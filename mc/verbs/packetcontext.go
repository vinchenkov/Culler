package verbs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// maxPacketReportTextBytes is the size limit of the report text in a packet
// context, after the replacement of the media.
const maxPacketReportTextBytes = 128 << 10

// packetMediaAddress matches a data address. The word boundary keeps an
// address such as "metadata:" intact.
var packetMediaAddress = regexp.MustCompile(`(?i)\bdata:([a-z0-9!#$&^_.+-]+/[a-z0-9!#$&^_.+-]+)?((?:;[a-z0-9!#$&^_.+=-]+)*?)(;base64)?,([^"'\s()<>]*)`)

// HomiePacketContext returns the packet context of a packet conversation. The
// Homie runner reads it at the first turn of each native session.
func HomiePacketContext(db *sql.DB, id *RunIdentity, sessionID string) (any, error) {
	if err := requireHomieRunner(id, sessionID, "mc homie packet-context"); err != nil {
		return nil, err
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, classify(err)
	}
	defer tx.Rollback()
	var packet, version sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		SELECT s.packet_id, p.version_id FROM homie_sessions s
		LEFT JOIN review_versions p ON p.id = s.packet_id
		WHERE s.id = ?`, sessionID).Scan(&packet, &version)
	if err == sql.ErrNoRows {
		return nil, Domainf("unknown Homie session %q", sessionID)
	}
	if err != nil {
		return nil, classify(err)
	}
	if !packet.Valid {
		return nil, Domainf("Homie session %q is not a packet conversation", sessionID)
	}
	text, err := BuildPacketContext(ctx, tx, ArtifactsMountRoot(), packet.Int64)
	if err != nil {
		return nil, classify(err)
	}
	return map[string]any{
		"session_id": sessionID, "packet_id": packet.Int64, "version_id": version.Int64, "context": text,
	}, nil
}

// BuildPacketContext describes one packet as the operator reviewed it: its
// identity, its content in full up to the size limit, and the identifiers of
// the records that Homie reads on demand. The artifact paths resolve under
// artifactsRoot.
func BuildPacketContext(ctx context.Context, q Q, artifactsRoot string, packetID int64) (string, error) {
	var project, projectTitle, taskTitle, thesis, criteria, renderPath, reportDigest, inputCommit, resultCommit string
	var task, version int64
	var branch, destination, candidate, inventory, patch, evidence sql.NullString
	err := q.QueryRowContext(ctx, `
		SELECT t.project, pr.title, p.task_id, t.title, p.version_id,
		       p.thesis, p.criteria, p.render_path, p.report_sha256,
		       v.input_commit, v.result_commit,
		       c.destination_branch, c.destination_commit, c.candidate_commit,
		       json_extract(c.result, '$.changes.candidate.inventory_path'),
		       json_extract(c.result, '$.changes.candidate.patch_path'),
		       (SELECT evidence_path FROM version_verdicts WHERE version_id = p.version_id)
		FROM review_versions p
		JOIN tasks t ON t.id = p.task_id
		JOIN projects pr ON pr.id = t.project
		JOIN task_versions v ON v.id = p.version_id
		LEFT JOIN merge_candidates c ON c.id = p.candidate_id
		WHERE p.id = ?`, packetID).Scan(&project, &projectTitle, &task, &taskTitle, &version,
		&thesis, &criteria, &renderPath, &reportDigest, &inputCommit, &resultCommit,
		&branch, &destination, &candidate, &inventory, &patch, &evidence)
	if err == sql.ErrNoRows {
		return "", Domainf("The review packet %d is unavailable.", packetID)
	}
	if err != nil {
		return "", err
	}
	report, err := packetReport(ctx, q, artifactsRoot, task, renderPath, reportDigest)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Packet context\n\n")
	b.WriteString("This conversation belongs to the packet below. The packet content is complete up to its size limit, and each identifier names a record that you read on demand.\n\n")
	b.WriteString("## Packet identity\n\n")
	fmt.Fprintf(&b, "- Project: %s (%s)\n", project, projectTitle)
	fmt.Fprintf(&b, "- Task: %d (%s)\n", task, taskTitle)
	fmt.Fprintf(&b, "- Packet identifier: %d\n", packetID)
	fmt.Fprintf(&b, "- Version identifier: %d\n\n", version)
	b.WriteString("## Packet content\n\n")
	fmt.Fprintf(&b, "### Thesis\n\n%s\n\n", strings.TrimRight(thesis, "\r\n"))
	fmt.Fprintf(&b, "### Criteria\n\n%s\n\n", strings.TrimRight(criteria, "\r\n"))
	b.WriteString("### Report\n\n")
	b.WriteString(report)
	b.WriteString("\n\n## Packet identifiers\n\n")
	line := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&b, "- %s: %s\n", name, value)
		}
	}
	file := func(name string, path sql.NullString) {
		if path.Valid && artifactFileReadable(artifactsRoot, path.String) {
			line(name, path.String)
		}
	}
	line("Input commit", inputCommit)
	line("Verified Worker commit", resultCommit)
	line("Destination branch", branch.String)
	line("Destination commit", destination.String)
	line("Merge candidate commit", candidate.String)
	file("Report file", sql.NullString{String: renderPath, Valid: renderPath != ""})
	file("Verifier evidence", evidence)
	file("Change data inventory", inventory)
	file("Change data patch", patch)
	return b.String(), nil
}

// packetReport is the body of the report part of a packet context: the fenced
// report text, or one statement of why the report text is unavailable.
func packetReport(ctx context.Context, q Q, artifactsRoot string, task int64, renderPath, digest string) (string, error) {
	if renderPath == "" {
		return "The packet records no report file.", nil
	}
	state, err := artifactState(ctx, q, task, renderPath)
	if err != nil {
		return "", err
	}
	if state["status"] == "expired" {
		return fmt.Sprintf("The report text is unavailable. The artifact files of this task expired on %v.", state["expired_at"]), nil
	}
	raw, ok := readArtifactFile(artifactsRoot, renderPath, maxPacketRenderBytes)
	if !ok {
		return fmt.Sprintf("The report text is unavailable. The report file %s is absent.", renderPath), nil
	}
	sum := sha256.Sum256(raw)
	if len(raw) > maxPacketRenderBytes || hex.EncodeToString(sum[:]) != digest {
		return fmt.Sprintf("The report text is unavailable. The report file %s differs from its recorded version.", renderPath), nil
	}
	text, size, cut := packetReportText(raw)
	fence := strings.Repeat("`", max(3, longestBacktickRun(text)+1))
	var b strings.Builder
	b.WriteString(fence + packetReportKind(renderPath) + "\n")
	b.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		b.WriteString("\n")
	}
	b.WriteString(fence)
	if cut {
		fmt.Fprintf(&b, "\n\nThe report text stops at %d of its %d bytes. The complete report file is %s.", maxPacketReportTextBytes, size, renderPath)
	}
	return b.String(), nil
}

// packetReportText replaces each data address of the report with a short
// reference and cuts the result at the size limit. The size is the byte count
// after the replacement.
func packetReportText(raw []byte) (text string, size int, cut bool) {
	text = strings.ToValidUTF8(string(raw), "�")
	var b strings.Builder
	last := 0
	for _, m := range packetMediaAddress.FindAllStringSubmatchIndex(text, -1) {
		b.WriteString(text[last:m[0]])
		mediaType := "text/plain"
		if m[2] >= 0 {
			mediaType = strings.ToLower(text[m[2]:m[3]])
		}
		payload := text[m[8]:m[9]]
		fmt.Fprintf(&b, "[media: %s, %d bytes]", mediaType, packetMediaSize(payload, m[6] >= 0))
		last = m[1]
	}
	b.WriteString(text[last:])
	text = b.String()
	size = len(text)
	if size <= maxPacketReportTextBytes {
		return text, size, false
	}
	end := maxPacketReportTextBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end], size, true
}

// packetMediaSize is the byte count of the media of one data address. A
// payload that does not decode counts its own text.
func packetMediaSize(payload string, encoded bool) int {
	if encoded {
		if media, err := base64.StdEncoding.DecodeString(payload); err == nil {
			return len(media)
		}
		if media, err := base64.RawStdEncoding.DecodeString(payload); err == nil {
			return len(media)
		}
		return len(payload)
	}
	if media, err := url.PathUnescape(payload); err == nil {
		return len(media)
	}
	return len(payload)
}

func packetReportKind(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm":
		return "html"
	case ".md", ".markdown":
		return "markdown"
	}
	return "text"
}

func longestBacktickRun(text string) int {
	longest, run := 0, 0
	for i := 0; i < len(text); i++ {
		if text[i] != '`' {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	return longest
}

// artifactRelative names a recorded artifact path inside the artifact root.
func artifactRelative(artifactsRoot, path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, artifactsRoot+"/")
	return rest, ok && filepath.IsLocal(rest)
}

// artifactFileReadable reports whether a recorded artifact path resolves to a
// regular file under the artifact root.
func artifactFileReadable(artifactsRoot, path string) bool {
	relative, ok := artifactRelative(artifactsRoot, path)
	if !ok {
		return false
	}
	root, err := os.OpenRoot(artifactsRoot)
	if err != nil {
		return false
	}
	defer root.Close()
	info, err := root.Stat(relative)
	return err == nil && info.Mode().IsRegular()
}

// readArtifactFile reads at most limit+1 bytes of a regular file under the
// artifact root. The final path element must not be a symbolic link, and the
// opened file must be the file that the check saw.
func readArtifactFile(artifactsRoot, path string, limit int64) ([]byte, bool) {
	relative, ok := artifactRelative(artifactsRoot, path)
	if !ok {
		return nil, false
	}
	root, err := os.OpenRoot(artifactsRoot)
	if err != nil {
		return nil, false
	}
	defer root.Close()
	checked, err := root.Lstat(relative)
	if err != nil || !checked.Mode().IsRegular() {
		return nil, false
	}
	file, err := root.Open(relative)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(checked, opened) {
		return nil, false
	}
	var raw bytes.Buffer
	if _, err := io.Copy(&raw, io.LimitReader(file, limit+1)); err != nil {
		return nil, false
	}
	return raw.Bytes(), true
}
