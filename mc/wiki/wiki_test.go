package wiki

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"mc/repository"
)

type wikiRepository struct {
	t    *testing.T
	root string
}

func newWikiRepository(t *testing.T) wikiRepository {
	t.Helper()
	r := wikiRepository{t: t, root: t.TempDir()}
	r.git("init", "--quiet", "--template=", "-b", "main")
	return r
}

func (r wikiRepository) git(args ...string) string {
	r.t.Helper()
	fixed := []string{"-C", r.root, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false",
		"-c", "user.name=Wiki Fixture", "-c", "user.email=fixture@example.test"}
	cmd := exec.Command("git", append(fixed, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + r.root,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C"}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	body, err := cmd.Output()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, stderr.String())
	}
	return strings.TrimSpace(string(body))
}

func (r wikiRepository) write(name, body string) {
	r.t.Helper()
	filename := filepath.Join(r.root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(body), 0644); err != nil {
		r.t.Fatal(err)
	}
}

func (r wikiRepository) commit(message string) string {
	r.t.Helper()
	r.git("add", "--all")
	r.git("commit", "--quiet", "--allow-empty", "-m", message)
	return r.git("rev-parse", "HEAD")
}

func nodeByID(t *testing.T, view View, id string) Node {
	t.Helper()
	for _, node := range view.Graph.Nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("the graph lacks document %q: %#v", id, view.Graph.Nodes)
	return Node{}
}

func TestReadAcceptedDocumentsAndLinks(t *testing.T) {
	r := newWikiRepository(t)
	report := "# Complete research report\n\n" + strings.Repeat("The report retains its evidence and limitations.\n", 400) +
		"\n[Authentication](../code/auth.md#flow)\n[Original evidence](https://example.test/source)\n"
	r.write("openwiki/reports/result.md", "---\ntitle: 'Research: authentication'\ntype: Research\ndescription: Complete accepted evidence.\ntags:\n  - evidence\n  - auth\n---\n"+report)
	r.write("openwiki/code/auth.md", "---\ntitle: Authentication\ntype: Component\ntags: [auth, access]\n---\n# Authentication\n\n[Report](../reports/result.md#evidence)\n[Report again](../reports/result.md)\n")
	r.write("openwiki/index.md", "# Project wiki\n\n[Report](reports/result.md)\n")
	r.write("openwiki/.claims/auth.json", "{\"claims\":[]}")
	r.write("openwiki/.private.md", "Private metadata.")
	r.write("openwiki/INSTRUCTIONS.md", "The user's wiki brief.")
	r.write("openwiki/log.md", "An automatic operation record.")
	r.write("source.txt", "Verified application source.\n")
	accepted := r.commit("Accept the wiki")

	view, err := Read(context.Background(), r.root, "ws-report")
	if err != nil {
		t.Fatal(err)
	}
	if view.Project != "ws-report" || view.AcceptedCommit != accepted || view.Status != "ready" {
		t.Fatalf("the view identifies the wrong accepted version: %#v", view)
	}
	if len(view.Graph.Nodes) != 3 || !reflect.DeepEqual(view.Graph.Types, []string{"Component", "Research", "Section"}) {
		t.Fatalf("the graph lost the document types or exposed metadata: %#v", view.Graph)
	}
	node := nodeByID(t, view, "reports/result")
	if node.Body != report || node.Size != len(report) || node.Title != "Research: authentication" || node.Type != "Research" {
		t.Fatalf("the reader changed the report: title=%q type=%q size=%d", node.Title, node.Type, node.Size)
	}
	if !reflect.DeepEqual(node.Tags, []string{"evidence", "auth"}) || node.Description != "Complete accepted evidence." {
		t.Fatalf("the reader changed the report metadata: %#v", node)
	}
	if !reflect.DeepEqual(node.Links, []string{"code/auth"}) || !reflect.DeepEqual(node.Backlinks, []string{"code/auth", "index"}) {
		t.Fatalf("the reader lost the document links: links=%v backlinks=%v", node.Links, node.Backlinks)
	}
	auth := nodeByID(t, view, "code/auth")
	if !reflect.DeepEqual(auth.Links, []string{"reports/result"}) || len(view.Graph.Edges) != 3 {
		t.Fatalf("the graph duplicates or loses links: %#v", view.Graph.Edges)
	}
	if !strings.Contains(node.Body, "https://example.test/source") {
		t.Fatal("the reader changed the external source link")
	}
}

func TestReadIgnoresCandidateAndDirtyFiles(t *testing.T) {
	r := newWikiRepository(t)
	r.write("openwiki/report.md", "---\ntitle: Accepted report\ntype: Research\n---\nAccepted evidence.\n")
	accepted := r.commit("Accept the report")
	r.git("checkout", "--quiet", "-b", "candidate")
	r.write("openwiki/report.md", "---\ntitle: Updated report\ntype: Research\n---\nUpdated evidence.\n")
	candidate := r.commit("Prepare a document change")
	r.write("openwiki/report.md", "Uncommitted replacement.\n")
	r.write("openwiki/draft.md", "Uncommitted draft.\n")
	before := r.git("status", "--porcelain=v1")

	view, err := Read(context.Background(), r.root, "ws-one")
	if err != nil {
		t.Fatal(err)
	}
	if view.AcceptedCommit != accepted || nodeByID(t, view, "report").Body != "Accepted evidence.\n" || len(view.Graph.Nodes) != 1 {
		t.Fatalf("the reader exposed unaccepted files: %#v", view)
	}
	r.git("update-ref", "refs/heads/main", candidate, accepted)
	updated, err := Read(context.Background(), r.root, "ws-one")
	if err != nil {
		t.Fatal(err)
	}
	if updated.AcceptedCommit != candidate || nodeByID(t, updated, "report").Body != "Updated evidence.\n" {
		t.Fatalf("the reader kept the previous accepted version: %#v", updated)
	}
	if after := r.git("status", "--porcelain=v1"); after != before {
		t.Fatalf("the reader changed the checkout: before=%q after=%q", before, after)
	}
	if current := r.git("rev-parse", "HEAD"); current != candidate {
		t.Fatalf("the reader changed the active commit: %s", current)
	}
}

func TestReadKeepsProjectDocumentsSeparate(t *testing.T) {
	for _, id := range []string{"ws-one", "ws-two"} {
		r := newWikiRepository(t)
		r.write("openwiki/report.md", "---\ntitle: "+id+"\ntype: Research\n---\nEvidence for "+id+".\n")
		commit := r.commit("Accept the project report")
		view, err := Read(context.Background(), r.root, id)
		if err != nil {
			t.Fatal(err)
		}
		if view.Project != id || view.AcceptedCommit != commit || nodeByID(t, view, "report").Body != "Evidence for "+id+".\n" {
			t.Fatalf("the reader used another Project: %#v", view)
		}
	}
}

func TestReadReportsAnAbsentWiki(t *testing.T) {
	r := newWikiRepository(t)
	r.write("source.txt", "The application source.\n")
	commit := r.commit("Create the project")
	view, err := Read(context.Background(), r.root, "ws-new")
	if err != nil {
		t.Fatal(err)
	}
	if view.Status != "absent" || view.AcceptedCommit != commit || len(view.Graph.Nodes) != 0 {
		t.Fatalf("the absent wiki has the wrong state: %#v", view)
	}
	body, err := json.Marshal(view)
	if err != nil || !strings.Contains(string(body), `"nodes":[]`) || !strings.Contains(string(body), `"edges":[]`) {
		t.Fatalf("the absent graph must use empty arrays: %s, %v", body, err)
	}
}

func TestReadRejectsInvalidDocumentFiles(t *testing.T) {
	for _, kind := range []string{"invalid-utf8", "symbolic-link"} {
		t.Run(kind, func(t *testing.T) {
			r := newWikiRepository(t)
			r.write("source.txt", "Private application source.\n")
			if kind == "invalid-utf8" {
				r.write("openwiki/report.md", string([]byte{0xff, 0xfe}))
			} else {
				if err := os.Mkdir(filepath.Join(r.root, "openwiki"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../source.txt", filepath.Join(r.root, "openwiki/report.md")); err != nil {
					t.Fatal(err)
				}
			}
			r.commit("Create the invalid wiki fixture")
			if _, err := Read(context.Background(), r.root, "ws-invalid"); err == nil || !strings.Contains(err.Error(), "openwiki/report.md") {
				t.Fatalf("the reader accepted an invalid document: %v", err)
			}
		})
	}
}

type completionFixture struct {
	repo    wikiRepository
	input   string
	source  string
	result  string
	receipt Completion
	path    string
}

func newCompletionFixture(t *testing.T) *completionFixture {
	t.Helper()
	r := newWikiRepository(t)
	r.write("source.txt", "Verified application source.\n")
	r.write("AGENTS.md", "Preserve this user instruction.\n<!-- OPENWIKI:START -->\nPrevious wiki instructions.\n<!-- OPENWIKI:END -->\n")
	r.write("openwiki/INSTRUCTIONS.md", "Preserve this user brief.\n")
	r.write("openwiki/reports/task.md", "---\ntitle: Task report\ntype: Research\ndocument: true\n---\nComplete accepted report.\n")
	input := r.commit("Accept the source and report")
	source := input
	r.write("openwiki/index.md", "# Project wiki\n\n[Task report](reports/task.md)\n")
	r.write("openwiki/.last-update.json", "{\"status\":\"complete\"}\n")
	r.write("openwiki/.page-manifest.json", "{\"pages\":{}}\n")
	result := r.commit("Complete the wiki")
	files, err := readFiles(context.Background(), repository.Git{Root: r.root}, result)
	if err != nil {
		t.Fatal(err)
	}
	f := &completionFixture{repo: r, input: input, source: source, result: result, path: filepath.Join(t.TempDir(), "completion.json")}
	f.receipt = Completion{Format: 1, Status: "complete", InputCommit: input, SourceCommit: source,
		WikiDigest: Digest(files), OpenWikiVersion: Version, OpenWikiRevision: Revision}
	f.writeReceipt()
	return f
}

func (f *completionFixture) writeReceipt() {
	f.repo.t.Helper()
	body, err := json.Marshal(f.receipt)
	if err != nil {
		f.repo.t.Fatal(err)
	}
	if err := os.WriteFile(f.path, append(body, '\n'), 0600); err != nil {
		f.repo.t.Fatal(err)
	}
}

func (f *completionFixture) validate() error {
	_, err := ValidateCompletion(context.Background(), repository.Git{Root: f.repo.root}, f.input, f.result, f.path)
	return err
}

func TestCompletionAcceptsTheExactFinishedWiki(t *testing.T) {
	f := newCompletionFixture(t)
	if err := f.validate(); err != nil {
		t.Fatal(err)
	}
	f.input, f.source = f.result, f.result
	f.receipt.InputCommit, f.receipt.SourceCommit, f.receipt.Status = f.result, f.result, "noop"
	f.writeReceipt()
	if err := f.validate(); err != nil {
		t.Fatalf("the unchanged completed wiki lost its applicable receipt: %v", err)
	}
}

func TestCompletionRejectsAReceiptForAnotherRunOrBuild(t *testing.T) {
	f := newCompletionFixture(t)
	original := f.receipt
	for _, change := range []struct {
		name string
		edit func(*Completion)
	}{
		{"input", func(c *Completion) { c.InputCommit = strings.Repeat("f", 40) }},
		{"version", func(c *Completion) { c.OpenWikiVersion = "0.0.0" }},
		{"revision", func(c *Completion) { c.OpenWikiRevision = "unknown" }},
		{"status", func(c *Completion) { c.Status = "incomplete" }},
		{"format", func(c *Completion) { c.Format = 2 }},
	} {
		t.Run(change.name, func(t *testing.T) {
			f.receipt = original
			change.edit(&f.receipt)
			f.writeReceipt()
			if err := f.validate(); err == nil || err.Error() != "The OpenWiki completion receipt names another start commit, another OpenWiki version, or an incomplete run." {
				t.Fatalf("the validator accepted another run or build: %v", err)
			}
		})
	}
}

func TestCompletionRequiresSourceAncestry(t *testing.T) {
	f := newCompletionFixture(t)
	f.repo.git("checkout", "--quiet", "--orphan", "other-source")
	f.repo.write("source.txt", "Unrelated source.\n")
	f.receipt.SourceCommit = f.repo.commit("Create unrelated evidence")
	f.writeReceipt()
	outside := "The source commit of the OpenWiki run is outside the history of the wiki commit."
	if err := f.validate(); err == nil || err.Error() != outside {
		t.Fatalf("the validator accepted unrelated source evidence: %v", err)
	}

	f.repo.git("checkout", "--quiet", "main")
	f.repo.write("openwiki/reports/later.md", "Later evidence.\n")
	f.receipt.SourceCommit = f.repo.commit("Create evidence after the result")
	f.writeReceipt()
	if err := f.validate(); err == nil || err.Error() != outside {
		t.Fatalf("the validator accepted evidence after the submitted result: %v", err)
	}
}

func TestCompletionRejectsChangesAfterTheReceipt(t *testing.T) {
	f := newCompletionFixture(t)
	f.repo.write("openwiki/index.md", "Replacement after the completed page workflow.\n")
	f.result = f.repo.commit("Change the completed wiki")
	if err := f.validate(); err == nil || err.Error() != "The wiki files of the wiki commit differ from the files of the complete OpenWiki run." {
		t.Fatalf("the validator accepted wiki changes after completion: %v", err)
	}
}

func TestCompletionRejectsProjectDocumentDrift(t *testing.T) {
	f := newCompletionFixture(t)
	f.repo.write("openwiki/reports/task.md", "The evidence changed after page work.\n")
	f.result = f.repo.commit("Change the source evidence")
	if err := f.validate(); err == nil {
		t.Fatal("the validator accepted source evidence that changed after the baseline")
	}
}

func TestCompletionLimitsRepositoryChanges(t *testing.T) {
	for _, name := range []string{"source.txt", ".github/workflows/wiki.yml", "openwiki/INSTRUCTIONS.md"} {
		t.Run(name, func(t *testing.T) {
			f := newCompletionFixture(t)
			f.repo.write(name, "Unauthorized Scribe change.\n")
			f.result = f.repo.commit("Change a protected file")
			if err := f.validate(); err == nil || !strings.Contains(err.Error(), name) && name != "openwiki/INSTRUCTIONS.md" {
				t.Fatalf("the validator accepted a protected file change: %v", err)
			}
		})
	}
}

func TestCompletionPreservesUserInstructions(t *testing.T) {
	for _, body := range []string{
		"Replacement user instructions.\n<!-- OPENWIKI:START -->\nRead the wiki.\n<!-- OPENWIKI:END -->\n",
		"Preserve this user instruction.\n<!-- OPENWIKI:START -->\nAn incomplete managed section.\n",
		"Preserve this user instruction.\n<!-- OPENWIKI:START --><!-- OPENWIKI:END --><!-- OPENWIKI:START --><!-- OPENWIKI:END -->\n",
	} {
		f := newCompletionFixture(t)
		f.repo.write("AGENTS.md", body)
		f.result = f.repo.commit("Change the instruction fixture")
		if err := f.validate(); err == nil {
			t.Fatal("the validator accepted changed user instructions or invalid managed sections")
		}
	}
}

func TestCompletionRejectsANewManagedInstructionFile(t *testing.T) {
	f := newCompletionFixture(t)
	f.repo.write("CLAUDE.md", "<!-- OPENWIKI:START -->\nRead the shared wiki.\n<!-- OPENWIKI:END -->\n")
	f.result = f.repo.commit("Add the managed instruction file")
	if err := f.validate(); err == nil {
		t.Fatal("the validator accepted an instruction file outside the wiki")
	}
}

func TestCompletionPreservesInstructionFilePermissions(t *testing.T) {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		t.Run(name, func(t *testing.T) {
			f := newCompletionFixture(t)
			if name == "CLAUDE.md" {
				f.repo.write(name, "<!-- OPENWIKI:START -->\nRead the shared wiki.\n<!-- OPENWIKI:END -->\n")
			}
			if err := os.Chmod(filepath.Join(f.repo.root, name), 0755); err != nil {
				t.Fatal(err)
			}
			f.result = f.repo.commit("Make the instruction file executable")
			if err := f.validate(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("the validator accepted executable instruction changes: %v", err)
			}
		})
	}
}

func TestCompletionRejectsIncompleteQueues(t *testing.T) {
	for _, name := range []string{"openwiki/.run.json", "openwiki/.culler-run.json"} {
		t.Run(name, func(t *testing.T) {
			f := newCompletionFixture(t)
			f.repo.write(name, "{\"status\":\"incomplete\"}\n")
			f.result = f.repo.commit("Preserve an incomplete queue")
			files, err := readFiles(context.Background(), repository.Git{Root: f.repo.root}, f.result)
			if err != nil {
				t.Fatal(err)
			}
			f.receipt.WikiDigest = Digest(files)
			f.writeReceipt()
			if err := f.validate(); err == nil || !strings.Contains(err.Error(), "incomplete") {
				t.Fatalf("the validator accepted an incomplete page queue: %v", err)
			}
		})
	}
}

func TestCompletionRejectsExtraReceiptData(t *testing.T) {
	f := newCompletionFixture(t)
	body, err := os.ReadFile(f.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"{}", strings.Repeat(" ", 16385) + "{}"} {
		if err := os.WriteFile(f.path, append(append([]byte{}, body...), suffix...), 0600); err != nil {
			t.Fatal(err)
		}
		if err := f.validate(); err == nil || err.Error() != "The OpenWiki completion receipt is invalid." {
			t.Fatalf("the validator accepted extra receipt data: %v", err)
		}
	}
}

func TestCompletionRejectsSymbolicLinkAndNamedPipeReceipts(t *testing.T) {
	f := newCompletionFixture(t)
	original := f.path
	for _, kind := range []string{"symbolic link", "named pipe"} {
		t.Run(kind, func(t *testing.T) {
			f.path = filepath.Join(t.TempDir(), "receipt")
			var err error
			if kind == "symbolic link" {
				err = os.Symlink(original, f.path)
			} else {
				err = syscall.Mkfifo(f.path, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := f.validate(); err == nil || !strings.Contains(err.Error(), "regular file") {
				t.Fatalf("the validator accepted a %s receipt: %v", kind, err)
			}
		})
	}
}

func TestCompletionRejectsSourceChangesThatTheResultReverts(t *testing.T) {
	f := newCompletionFixture(t)
	f.repo.write("source.txt", "The Scribe changed the application source.\n")
	f.receipt.SourceCommit = f.repo.commit("Change the source before page work")
	f.repo.write("source.txt", "Verified application source.\n")
	f.result = f.repo.commit("Restore the accepted source after page work")
	f.writeReceipt()
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "source.txt") {
		t.Fatalf("the validator accepted Claims from an unauthorized source baseline: %v", err)
	}
}

func TestReadIncludesCompleteImportedDocuments(t *testing.T) {
	r := newWikiRepository(t)
	r.write("openwiki/index.md", "# Project\n\n[Complete report](../docs/research/result.md)\n")
	original := "# Research report\n\nThe complete research result.\n\n[Spec](../../specs/design.md)\n[Wiki](/openwiki/index.md)\n"
	r.write("docs/research/result.md", original)
	r.write("specs/design.md", "# Design\n\nThe complete design.\n")
	accepted := r.commit("Accept project documents")
	r.git("checkout", "--quiet", "-b", "task")
	r.write("docs/research/result.md", "Unaccepted report.\n")
	r.commit("Revise the report")
	view, err := Read(context.Background(), r.root, "imported")
	if err != nil {
		t.Fatal(err)
	}
	report := nodeByID(t, view, "../docs/research/result")
	if report.Body != original || view.AcceptedCommit != accepted || len(view.Graph.Nodes) != 3 {
		t.Fatalf("the reader lost the authoritative document: %+v", view)
	}
	if !reflect.DeepEqual(report.Links, []string{"../specs/design", "index"}) {
		t.Fatal(report.Links)
	}
}

func TestScribePreservesImportedDocuments(t *testing.T) {
	r := newWikiRepository(t)
	r.write("docs/report.md", "# Report\n\nThe complete verified report.\n")
	input := r.commit("Submit the report")
	r.write("openwiki/index.md", "# Project\n\n[Report](../docs/report.md)\n")
	r.write("openwiki/.last-update.json", "{\"status\":\"complete\"}\n")
	result := r.commit("Index the report")
	g := repository.Git{Root: r.root}
	files, err := readFiles(context.Background(), g, result)
	if err != nil {
		t.Fatal(err)
	}
	f := &completionFixture{repo: r, input: input, source: input, result: result, path: filepath.Join(t.TempDir(), "receipt.json")}
	f.receipt = Completion{Format: 1, Status: "complete", InputCommit: input, SourceCommit: input, WikiDigest: Digest(files), OpenWikiVersion: Version, OpenWikiRevision: Revision}
	f.writeReceipt()
	if err := f.validate(); err != nil {
		t.Fatal(err)
	}
	r.write("docs/report.md", "# Report\n\nA Scribe revision.\n")
	f.result = r.commit("Change the imported report")
	if err := f.validate(); err == nil {
		t.Fatal("the Scribe changed an imported document")
	}
}

func TestScribePreservesReportContentBeforeTheSourceBaseline(t *testing.T) {
	for _, change := range []struct {
		name, body string
		permitted  bool
	}{
		{"metadata", "---\ntitle: Updated title\ntype: Research\ndocument: true\ntags: [research]\n---\nComplete accepted report.\n", true},
		{"content", "---\ntitle: Task report\ntype: Research\ndocument: true\n---\nA different conclusion.\n", false},
		{"identity", "---\ntitle: Task report\ntype: Research\n---\nComplete accepted report.\n", false},
		{"provenance", "---\ntitle: Task report\ntype: Research\ndocument: true\ntask_id: 99\n---\nComplete accepted report.\n", false},
	} {
		t.Run(change.name, func(t *testing.T) {
			f := newCompletionFixture(t)
			f.repo.write("openwiki/reports/task.md", change.body)
			f.result = f.repo.commit("Apply the document change")
			files, err := readFiles(context.Background(), repository.Git{Root: f.repo.root}, f.result)
			if err != nil {
				t.Fatal(err)
			}
			f.receipt.SourceCommit = f.result
			f.receipt.WikiDigest = Digest(files)
			f.writeReceipt()
			if err := f.validate(); (err == nil) != change.permitted {
				t.Fatalf("permitted=%v: %v", change.permitted, err)
			}
		})
	}
}

func TestScribeRepairsMetadataAndPreservesAnUnmarkedWorkerReport(t *testing.T) {
	r := newWikiRepository(t)
	r.write("openwiki/report.md", "# Report\n\nThe verified conclusion.\n")
	input := r.commit("Submit the report")
	r.write("openwiki/report.md", "---\ntitle: Report\ntype: Research\ndocument: true\n---\n# Report\n\nThe verified conclusion.\n")
	source := r.commit("Add the wiki metadata")
	r.write("openwiki/index.md", "# Project\n")
	r.write("openwiki/.last-update.json", "{\"status\":\"complete\"}\n")
	result := r.commit("Complete the wiki")
	g := repository.Git{Root: r.root}
	files, err := readFiles(context.Background(), g, result)
	if err != nil {
		t.Fatal(err)
	}
	f := &completionFixture{repo: r, input: input, source: source, result: result, path: filepath.Join(t.TempDir(), "receipt.json")}
	f.receipt = Completion{Format: 1, Status: "complete", InputCommit: input, SourceCommit: source, WikiDigest: Digest(files), OpenWikiVersion: Version, OpenWikiRevision: Revision}
	f.writeReceipt()
	if _, err := ValidateCompletion(context.Background(), g, input, result, f.path); err != nil {
		t.Fatal(err)
	}
	r.write("openwiki/report.md", "---\ntitle: Report\ntype: Research\ndocument: true\n---\nA different conclusion.\n")
	f.result = r.commit("Revise the report content")
	f.receipt.SourceCommit = f.result
	f.writeReceipt()
	if _, err := ValidateCompletion(context.Background(), g, input, f.result, f.path); err == nil {
		t.Fatal("metadata repair replaced the verified report")
	}
}
