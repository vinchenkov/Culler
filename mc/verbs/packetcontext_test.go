package verbs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/dispatch"
)

// pcFixture is one packet whose recorded artifact paths resolve under a
// temporary artifact root.
type pcFixture struct {
	db                                 *sql.DB
	root                               string
	packet, version                    int64
	report, evidence, inventory, patch string
}

const (
	pcInput     = "1111111111111111111111111111111111111111"
	pcResult    = "2222222222222222222222222222222222222222"
	pcCandidate = "3333333333333333333333333333333333333333"
)

// newPCFixture records the delivery of task 1 and publishes its packet. The
// report file holds the report bytes. Without a candidate, the packet records
// no merge candidate.
func newPCFixture(t *testing.T, reportName string, report []byte, candidate bool) *pcFixture {
	t.Helper()
	db := dvSpine(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_ARTIFACTS_ROOT", root)
	folder := filepath.Join(root, "ws-test", ".mc-artifacts", "1")
	f := &pcFixture{
		db: db, root: root,
		report:    filepath.Join(folder, "packet", reportName),
		evidence:  filepath.Join(folder, "verification", "verdict.md"),
		inventory: filepath.Join(folder, "changes", "context-1-merge", "candidate.json"),
		patch:     filepath.Join(folder, "changes", "context-1-merge", "candidate.patch"),
	}
	for path, body := range map[string][]byte{
		f.report: report, f.evidence: []byte("The Verifier evidence text.\n"),
		f.inventory: []byte(`{"files":[]}`), f.patch: []byte("diff --git a/x b/x\n"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	sum := sha256.Sum256(report)

	dvInsertTask(t, db, dvTask(1, dispatch.ScopeTask, dispatch.StatusVerified, 0))
	dvExec(t, db, `UPDATE tasks SET title='Add the export command', description='- The export command writes one file.' WHERE id=1`)
	worker, verifier, merge := "context-1-worker", "context-1-verifier", "context-1-merge"
	for _, run := range []struct{ id, role string }{{worker, "worker"}, {verifier, "verifier"}, {merge, "packager"}} {
		dvExec(t, db, `INSERT INTO runs (id, tier, role, project, subject, ended_at)
			VALUES (?, 'pipeline', ?, 'ws-test', 1, datetime('now'))`, run.id, run.role)
	}
	dvExec(t, db, `INSERT INTO repository_runs (run_id, task_id, input_commit, destination_commit, temporary_bytes, captured, cleaned)
		VALUES (?, 1, ?, ?, 0, 1, 1)`, worker, pcInput, pcInput)
	dvExec(t, db, `UPDATE tasks SET input_commit=? WHERE id=1`, pcInput)
	dvExec(t, db, `INSERT INTO version_submissions (run_id, task_id, input_commit, result_commit) VALUES (?, 1, ?, ?)`, worker, pcInput, pcResult)
	dvExec(t, db, `INSERT INTO task_versions (task_id, run_id, input_commit, result_commit, destination_branch, retained_ref)
		VALUES (1, ?, ?, ?, 'culler', 'refs/mc/fixture/context-1-worker')`, worker, pcInput, pcResult)
	version := dfInt(t, db, `SELECT id FROM task_versions WHERE run_id=?`, worker)
	dvCaptureVersion(t, db, worker, version)
	dvExec(t, db, `UPDATE repository_runs SET version_id=? WHERE run_id=?`, version, worker)
	dvExec(t, db, `UPDATE tasks SET submitted_version=? WHERE id=1`, version)
	dvExec(t, db, `INSERT INTO repository_runs (run_id, task_id, input_commit, destination_commit, version_id, temporary_bytes, captured, cleaned)
		VALUES (?, 1, ?, ?, ?, 0, 1, 1)`, verifier, pcResult, pcInput, version)
	dvExec(t, db, `INSERT INTO version_verdicts (version_id, run_id, outcome, evidence_path) VALUES (?, ?, 'pass', ?)`, version, verifier, f.evidence)
	dvExec(t, db, `UPDATE tasks SET source_version=?, report_path=?, report_sha256=?, thesis='The export command writes the report.' WHERE id=1`,
		version, f.report, hex.EncodeToString(sum[:]))
	if candidate {
		changes, err := json.Marshal(map[string]any{"changes": map[string]any{"candidate": map[string]any{
			"from_commit": pcInput, "to_commit": pcCandidate, "inventory_path": f.inventory, "patch_path": f.patch,
		}}})
		if err != nil {
			t.Fatal(err)
		}
		dvExec(t, db, `INSERT INTO merge_candidates (task_id, run_id, version_id, destination_branch, destination_commit, candidate_commit, result, status, retained_ref)
			VALUES (1, ?, ?, 'culler', ?, ?, ?, 'merged', 'refs/mc/fixture/context-1-merge')`, merge, version, pcInput, pcCandidate, string(changes))
		dvExec(t, db, `UPDATE tasks SET candidate_id=(SELECT id FROM merge_candidates WHERE run_id=?) WHERE id=1`, merge)
	}
	dvExec(t, db, `UPDATE tasks SET status='packaged' WHERE id=1`)
	f.packet, f.version = dvPublishPacket(t, db, 1, f.report, dvOld)
	return f
}

func (f *pcFixture) build(t *testing.T) string {
	t.Helper()
	text, err := BuildPacketContext(context.Background(), f.db, f.root, f.packet)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestPacketContextHoldsTheIdentityTheContentAndTheIdentifiers(t *testing.T) {
	f := newPCFixture(t, "packet.md", []byte("# Report\n\nThe command writes `export.json`.\n"), true)
	var title string
	hwGet(t, f.db, `SELECT title FROM projects WHERE id='ws-test'`, &title)
	want := "# Packet context\n\n" +
		"This conversation belongs to the packet below. The packet content is complete up to its size limit, and each identifier names a record that you read on demand.\n\n" +
		"## Packet identity\n\n" +
		"- Project: ws-test (" + title + ")\n" +
		"- Task: 1 (Add the export command)\n" +
		fmt.Sprintf("- Packet identifier: %d\n", f.packet) +
		fmt.Sprintf("- Version identifier: %d\n\n", f.version) +
		"## Packet content\n\n" +
		"### Thesis\n\nThe export command writes the report.\n\n" +
		"### Criteria\n\n- The export command writes one file.\n\n" +
		"### Report\n\n" +
		"```markdown\n# Report\n\nThe command writes `export.json`.\n```\n\n" +
		"## Packet identifiers\n\n" +
		"- Input commit: " + pcInput + "\n" +
		"- Verified Worker commit: " + pcResult + "\n" +
		"- Destination branch: culler\n" +
		"- Destination commit: " + pcInput + "\n" +
		"- Merge candidate commit: " + pcCandidate + "\n" +
		"- Report file: " + f.report + "\n" +
		"- Verifier evidence: " + f.evidence + "\n" +
		"- Change data inventory: " + f.inventory + "\n" +
		"- Change data patch: " + f.patch + "\n"
	if got := f.build(t); got != want {
		t.Fatalf("the packet context is\n%s\nwant\n%s", got, want)
	}

	// The verb reads the artifact files under the artifact root of the container.
	session := hpMustPrepare(t, f.db, f.packet, f.version, false).SessionID
	result, err := ColliePacketContext(f.db, &RunIdentity{Tier: "collie", Role: "collie", RunID: session}, session)
	if err != nil {
		t.Fatal(err)
	}
	if result.(map[string]any)["context"] != want {
		t.Fatalf("the verb gave the packet context %v", result)
	}
}

func TestPacketContextHoldsNoReviewStateNoMergeStateAndNoBlock(t *testing.T) {
	f := newPCFixture(t, "packet.md", []byte("The report.\n"), true)
	before := f.build(t)
	if _, err := TaskBlock(f.db, nil, 1, "The operator must confirm the license."); err != nil {
		t.Fatal(err)
	}
	if after := f.build(t); after != before {
		t.Fatalf("a block changed the packet context:\n%s\nwant\n%s", after, before)
	}
	for _, absent := range []string{"blocked", "The operator must confirm the license.", "awaits_decision", "review_state",
		"merge_state", "Culler merges", "The Verifier evidence text.", "diff --git", "context-1-worker", "context-1-verifier"} {
		if strings.Contains(before, absent) {
			t.Errorf("the packet context holds %q", absent)
		}
	}
}

func TestPacketContextReplacesEachDataAddress(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfixture-pixels")
	encoded := base64.StdEncoding.EncodeToString(png)
	report := `<p>Before</p><img src="data:image/png;base64,` + encoded + `" alt="chart">` +
		`<style>.mark{background:url(data:image/svg+xml;charset=utf-8,%3Csvg%3E%3C%2Fsvg%3E)}</style>` +
		`<a href='data:,Hello%20world'>text</a><meta name="metadata:keep">`
	f := newPCFixture(t, "packet.html", []byte(report), true)
	text := f.build(t)
	for _, want := range []string{
		"```html\n<p>Before</p>",
		fmt.Sprintf(`<img src="[media: image/png, %d bytes]" alt="chart">`, len(png)),
		"url([media: image/svg+xml, 11 bytes])",
		`href='[media: text/plain, 11 bytes]'`,
		`name="metadata:keep"`,
		"- Report file: " + f.report + "\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the packet context lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, encoded) || strings.Contains(strings.ReplaceAll(text, "metadata:", ""), "data:") {
		t.Fatalf("the packet context holds a data address:\n%s", text)
	}
}

// The fence of the report text is longer than each backtick run in the text,
// so a fence in the report cannot close the report part early. The fence names
// the kind of the report file.
func TestPacketContextFencesTheReportText(t *testing.T) {
	for _, tc := range []struct {
		name, report, fence, kind string
	}{
		{"packet.markdown", "Run this:\n\n```sh\nmake\n```\n", "````", "markdown"},
		{"packet.htm", "<pre>`````</pre>\n", "``````", "html"},
		{"packet.log", "No backtick.\n", "```", "text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPCFixture(t, tc.name, []byte(tc.report), true)
			want := "### Report\n\n" + tc.fence + tc.kind + "\n" + tc.report + tc.fence + "\n\n## Packet identifiers\n\n"
			if text := f.build(t); !strings.Contains(text, want) {
				t.Fatalf("the packet context lacks %q:\n%s", want, text)
			}
		})
	}
}

func TestPacketContextCutsTheReportTextAt128KiB(t *testing.T) {
	// The media reference is shorter than its address, so the size after the
	// replacement is the size that the cut statement states.
	media := "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("m", 3000)))
	// The offset of one byte puts byte 131072 inside a two-byte character.
	body := "x" + strings.Repeat("é", 70000) + media + "\nend of the report\n"
	f := newPCFixture(t, "packet.txt", []byte(body), true)
	text := f.build(t)
	replaced := "x" + strings.Repeat("é", 70000) + "[media: text/plain, 3000 bytes]\nend of the report\n"
	statement := fmt.Sprintf("The report text stops at 131072 of its %d bytes. The complete report file is %s.", len(replaced), f.report)
	if !strings.Contains(text, "```text\nx"+strings.Repeat("é", 65535)+"\n```\n\n"+statement+"\n\n## Packet identifiers") {
		t.Fatalf("the packet context does not cut the report text at the UTF-8 boundary below 131072 bytes, or lacks %q", statement)
	}
	if strings.Contains(text, "end of the report") {
		t.Fatal("the packet context holds the report text after the cut")
	}
	text, size, cut := packetReportText([]byte(strings.Repeat("a", maxPacketReportTextBytes)))
	if cut || size != maxPacketReportTextBytes || len(text) != maxPacketReportTextBytes {
		t.Fatalf("a report text of exactly 128 KiB gave %d bytes, size %d, cut %t", len(text), size, cut)
	}
}

func TestPacketContextStatesAnUnavailableReport(t *testing.T) {
	for name, tc := range map[string]struct {
		change    func(t *testing.T, f *pcFixture)
		statement func(f *pcFixture) string
		listed    bool
	}{
		"absent": {
			change: func(t *testing.T, f *pcFixture) {
				if err := os.Remove(f.report); err != nil {
					t.Fatal(err)
				}
			},
			statement: func(f *pcFixture) string {
				return "The report text is unavailable. The report file " + f.report + " is absent."
			},
		},
		"expired": {
			change: func(t *testing.T, f *pcFixture) {
				dvExec(t, f.db, `INSERT INTO artifact_retention (task_id, expires_at, expired_at) VALUES (1, '2026-10-01 00:00:00', '2026-10-02 00:00:00')`)
				dvExec(t, f.db, `INSERT INTO task_artifacts (task_id, path, expired_at) VALUES (1, ?, '2026-10-02 00:00:00')`, f.report)
			},
			statement: func(*pcFixture) string {
				return "The report text is unavailable. The artifact files of this task expired on 2026-10-02 00:00:00."
			},
			listed: true,
		},
		"digest": {
			change: func(t *testing.T, f *pcFixture) {
				if err := os.Chmod(f.report, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f.report, []byte("A replaced report.\n"), 0o444); err != nil {
					t.Fatal(err)
				}
			},
			statement: func(f *pcFixture) string {
				return "The report text is unavailable. The report file " + f.report + " differs from its recorded version."
			},
			listed: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newPCFixture(t, "packet.md", []byte("The secret report text.\n"), true)
			tc.change(t, f)
			text := f.build(t)
			for _, want := range []string{
				"### Thesis\n\nThe export command writes the report.\n\n",
				"### Criteria\n\n- The export command writes one file.\n\n",
				"### Report\n\n" + tc.statement(f) + "\n\n## Packet identifiers\n\n",
			} {
				if !strings.Contains(text, want) {
					t.Errorf("the packet context lacks %q:\n%s", want, text)
				}
			}
			if strings.Contains(text, "The secret report text.") || strings.Contains(text, "A replaced report.") || strings.Contains(text, "```") {
				t.Fatalf("the packet context holds report text:\n%s", text)
			}
			if listed := strings.Contains(text, "- Report file: "); listed != tc.listed {
				t.Fatalf("the report file line is present=%t, want %t:\n%s", listed, tc.listed, text)
			}
		})
	}
}

func TestPacketContextOmitsUnrecordedAndUnreadableIdentifiers(t *testing.T) {
	t.Run("no merge candidate", func(t *testing.T) {
		f := newPCFixture(t, "packet.md", []byte("The report.\n"), false)
		text := f.build(t)
		for _, absent := range []string{"Destination branch", "Destination commit", "Merge candidate commit", "Change data"} {
			if strings.Contains(text, absent) {
				t.Errorf("the packet context without a merge candidate holds %q", absent)
			}
		}
		for _, want := range []string{"- Input commit: " + pcInput + "\n", "- Verified Worker commit: " + pcResult + "\n", "- Verifier evidence: " + f.evidence + "\n"} {
			if !strings.Contains(text, want) {
				t.Errorf("the packet context lacks %q", want)
			}
		}
	})
	t.Run("unreadable artifact paths", func(t *testing.T) {
		f := newPCFixture(t, "packet.md", []byte("The report.\n"), true)
		outside := filepath.Join(t.TempDir(), "secret.md")
		if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(f.evidence); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, f.evidence); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(f.patch); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(f.inventory); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(f.inventory, 0o755); err != nil {
			t.Fatal(err)
		}
		text := f.build(t)
		for _, absent := range []string{"Verifier evidence", "Change data patch", "Change data inventory"} {
			if strings.Contains(text, absent) {
				t.Errorf("the packet context lists the unreadable %s:\n%s", absent, text)
			}
		}
		if !strings.Contains(text, "- Report file: "+f.report+"\n") || !strings.Contains(text, "- Merge candidate commit: "+pcCandidate+"\n") {
			t.Fatalf("the packet context lacks a readable identifier:\n%s", text)
		}
		// The recorded paths are outside this artifact root.
		other, err := BuildPacketContext(context.Background(), f.db, filepath.Dir(f.inventory), f.packet)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(other, "- Report file:") || !strings.Contains(other, "The report file "+f.report+" is absent.") {
			t.Fatalf("a path outside the artifact root resolved:\n%s", other)
		}
	})
}
