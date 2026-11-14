// Package wiki validates wiki submissions and reads accepted wiki documents.
package wiki

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"

	"mc/repository"
)

const Version = "0.9.0"
const Revision = "sha512-ARArQcKjvBWD56cAl0L5SGNmPfrE957zTPVvY+Hi1wVqBg7FqWWeqquPA4+fkA+iHAWclCScD9Snu7jUB9DCHw=="
const maxWikiBytes = 64 << 20

type Completion struct {
	Format           int    `json:"format"`
	Status           string `json:"status"`
	InputCommit      string `json:"input_commit"`
	SourceCommit     string `json:"source_commit"`
	WikiDigest       string `json:"wiki_digest"`
	OpenWikiVersion  string `json:"openwiki_version"`
	OpenWikiRevision string `json:"openwiki_revision"`
}

// ReceiptError reports a completion receipt that the check cannot read.
// The wiki commit has no check result, and a new submission can succeed.
type ReceiptError struct{ Text string }

func (e *ReceiptError) Error() string { return e.Text }

// CheckError reports a commit that violates a wiki rule.
type CheckError struct{ Text string }

func (e *CheckError) Error() string { return e.Text }

func checkFailed(format string, args ...any) error {
	return &CheckError{Text: fmt.Sprintf(format, args...)}
}

var errWikiReadLimit = errors.New("the wiki exceeds the 64 MiB read limit")

// entryError reports an entry under openwiki/ that holds no regular file data.
type entryError struct{ path, mode string }

func (e *entryError) Error() string {
	return "the repository file must contain regular file data: " + e.path
}

// kind names the entry in a check text.
func (e *entryError) kind() string {
	switch e.mode {
	case "120000":
		return "a symbolic link"
	case "160000":
		return "a submodule"
	}
	return "a file with a special file mode"
}

// scope names the two commits of a scope check in each check text.
type scope struct {
	base   string // the commit before the change
	result string // the commit after the change
	change string // the commit that a text about a change names
}

const (
	startCommit  = "start commit"
	sourceCommit = "source commit of the OpenWiki run"
	wikiCommit   = "wiki commit"
)

var (
	completionScope = scope{base: startCommit, result: wikiCommit, change: wikiCommit}
	// The wiki commit holds each change of the source commit of the OpenWiki run.
	sourceScope    = scope{base: startCommit, result: sourceCommit, change: wikiCommit}
	candidateScope = scope{base: "destination commit", result: "merge candidate", change: "merge candidate"}
)

// ValidateCompletion checks the wiki commit result against the start commit input and the completion receipt.
func ValidateCompletion(ctx context.Context, git repository.Git, input, result, receiptPath string) (Completion, error) {
	var receipt Completion
	unreadable := &ReceiptError{Text: "The OpenWiki completion receipt is not a readable regular file."}
	info, err := os.Lstat(receiptPath)
	if err != nil || !info.Mode().IsRegular() {
		return receipt, unreadable
	}
	file, err := os.OpenFile(receiptPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return receipt, unreadable
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return receipt, unreadable
	}
	body, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil {
		return receipt, unreadable
	}
	ancestor, err := git.IsAncestor(ctx, input, result)
	if err != nil {
		return receipt, err
	}
	if !ancestor {
		return receipt, checkFailed("The history of the wiki commit omits the start commit.")
	}
	invalid := checkFailed("The OpenWiki completion receipt is invalid.")
	if len(body) > 16384 {
		return receipt, invalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return receipt, invalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return receipt, invalid
	}
	if receipt.Format != 1 || (receipt.Status != "complete" && receipt.Status != "noop") ||
		receipt.OpenWikiVersion != Version || receipt.OpenWikiRevision != Revision || receipt.InputCommit != input {
		return receipt, checkFailed("The OpenWiki completion receipt names another start commit, another OpenWiki version, or an incomplete run.")
	}
	for _, pair := range [][2]string{{input, receipt.SourceCommit}, {receipt.SourceCommit, result}} {
		ancestor, err := git.IsAncestor(ctx, pair[0], pair[1])
		if err != nil {
			return receipt, err
		}
		if !ancestor {
			return receipt, checkFailed("The source commit of the OpenWiki run is outside the history of the wiki commit.")
		}
	}
	if err := validateScope(ctx, git, input, receipt.SourceCommit, sourceScope); err != nil {
		return receipt, err
	}
	if err := validateScope(ctx, git, input, result, completionScope); err != nil {
		return receipt, err
	}
	late, err := git.ChangedPaths(ctx, receipt.SourceCommit, result)
	if err != nil {
		return receipt, err
	}
	baseline, err := commitFiles(ctx, git, receipt.SourceCommit, sourceCommit)
	if err != nil {
		return receipt, err
	}
	for _, name := range late {
		if !strings.HasPrefix(name, "openwiki/") || projectDocument(baseline[name]) {
			return receipt, checkFailed("The wiki commit changes a project document after the OpenWiki run: %s.", name)
		}
	}
	files, err := commitFiles(ctx, git, result, wikiCommit)
	if err != nil {
		return receipt, err
	}
	if _, incomplete := files["openwiki/.run.json"]; incomplete {
		return receipt, checkFailed("The wiki commit holds the record of an incomplete OpenWiki page queue.")
	}
	if _, incomplete := files["openwiki/.culler-run.json"]; incomplete {
		return receipt, checkFailed("The wiki commit holds the record of incomplete document checks.")
	}
	if _, exists := files["openwiki/index.md"]; !exists {
		return receipt, checkFailed("The wiki commit lacks openwiki/index.md.")
	}
	var update struct {
		Status string `json:"status"`
	}
	if json.Unmarshal(files["openwiki/.last-update.json"], &update) != nil || update.Status != "complete" {
		return receipt, checkFailed("The wiki commit holds no record of a complete OpenWiki update.")
	}
	if Digest(files) != receipt.WikiDigest {
		return receipt, checkFailed("The wiki files of the wiki commit differ from the files of the complete OpenWiki run.")
	}
	return receipt, nil
}

func Digest(files map[string][]byte) string {
	paths := make([]string, 0, len(files))
	for name := range files {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, name := range paths {
		sum := sha256.Sum256(files[name])
		fmt.Fprintf(hash, "%s\x00%x\n", name, sum)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func readFiles(ctx context.Context, git repository.Git, commit string) (map[string][]byte, error) {
	entries, err := git.Tree(ctx, commit, "openwiki")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	var total int64
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Path, "openwiki/") {
			continue
		}
		if entry.Mode != "100644" && entry.Mode != "100755" {
			return nil, &entryError{path: entry.Path, mode: entry.Mode}
		}
		total += entry.Size
		if total > maxWikiBytes {
			return nil, errWikiReadLimit
		}
		body, err := git.ReadTreeFile(ctx, entry, maxWikiBytes)
		if err != nil {
			return nil, err
		}
		files[entry.Path] = body
	}
	return files, nil
}

// commitFiles reads the wiki files of one commit for a check.
// The holder names that commit in a check text.
func commitFiles(ctx context.Context, git repository.Git, commit, holder string) (map[string][]byte, error) {
	files, err := readFiles(ctx, git, commit)
	var entry *entryError
	switch {
	case errors.Is(err, errWikiReadLimit):
		return nil, checkFailed("The wiki of the %s exceeds the read limit of 64 MiB.", holder)
	case errors.As(err, &entry):
		return nil, checkFailed("The %s holds %s under openwiki/: %s.", holder, entry.kind(), entry.path)
	case errors.Is(err, repository.ErrTreeEntry):
		return nil, checkFailed("The %s holds a path under openwiki/ with a backslash or a line break.", holder)
	}
	return files, err
}

// ValidateMergeCandidate applies the scope rules of a wiki commit to the pair
// of the destination commit and the merge candidate of a wiki update.
// A CheckError reports a failed check.
func ValidateMergeCandidate(ctx context.Context, git repository.Git, destination, candidate string) error {
	return validateScope(ctx, git, destination, candidate, candidateScope)
}

func validateScope(ctx context.Context, git repository.Git, input, result string, names scope) error {
	changed, err := git.ChangedPaths(ctx, input, result)
	if err != nil {
		return err
	}
	before, err := commitFiles(ctx, git, input, names.base)
	if err != nil {
		return err
	}
	after, err := commitFiles(ctx, git, result, names.result)
	if err != nil {
		return err
	}
	for _, name := range changed {
		if !strings.HasPrefix(name, "openwiki/") {
			return checkFailed("The %s changes a path outside openwiki/: %s.", names.change, name)
		}
		if name == "openwiki/INSTRUCTIONS.md" {
			return checkFailed("The %s changes openwiki/INSTRUCTIONS.md.", names.change)
		}
		_, existed := before[name]
		sidecar := "openwiki/.claims/" + strings.TrimSuffix(strings.TrimPrefix(name, "openwiki/"), ".md") + ".json"
		_, generated := before[sidecar]
		original := projectDocument(before[name]) || (existed && !generated && !strings.HasPrefix(name, "openwiki/.") && !strings.HasSuffix(name, "/index.md") && name != "openwiki/log.md")
		if original {
			if _, claims := after[sidecar]; claims && strings.HasSuffix(name, ".md") {
				return checkFailed("The %s adds a Claim sidecar to a project document: %s.", names.change, name)
			}
			body, exists := after[name]
			if !exists || !bytes.Equal(documentContent(before[name]), documentContent(body)) || (projectDocument(before[name]) && !projectDocument(body)) {
				return checkFailed("The %s changes a protected part of a project document: %s.", names.change, name)
			}
		}
		if projectDocument(after[name]) && !original {
			return checkFailed("The %s adds a project document: %s.", names.change, name)
		}
		if projectDocument(after[name]) && generated {
			return checkFailed("The %s turns a generated page into a project document: %s.", names.change, name)
		}
		entries, err := git.Tree(ctx, result, name)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Mode != "100644" {
				return checkFailed("The %s holds a wiki file with a special file mode: %s.", names.change, name)
			}
		}
	}
	return nil
}

// documentContent preserves the body and all metadata except wiki display fields.
func documentContent(body []byte) []byte {
	text := string(body)
	_, _, content := frontmatter(text)
	if content == text {
		return append([]byte{0}, body...)
	}
	metadata := text[:len(text)-len(content)]
	lines := strings.SplitAfter(metadata, "\n")
	var preserved strings.Builder
	omitTags := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		trimmed := strings.TrimSpace(line)
		if omitTags && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) && strings.HasPrefix(trimmed, "- ") {
			continue
		}
		omitTags = false
		key, _, found := strings.Cut(line, ":")
		if found && (key == "title" || key == "type" || key == "tags" || key == "document") {
			omitTags = key == "tags"
			continue
		}
		preserved.WriteString(line)
	}
	preserved.WriteByte(0)
	preserved.WriteString(content)
	return []byte(preserved.String())
}

func projectDocument(body []byte) bool {
	meta, _, _ := frontmatter(string(body))
	return strings.EqualFold(meta["document"], "true")
}
