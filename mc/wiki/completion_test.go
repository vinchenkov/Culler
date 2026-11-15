package wiki

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mc/repository"
)

// commitResult commits the worktree as the wiki commit and writes a receipt for the files of that commit.
func (f *completionFixture) commitResult(message string) {
	f.repo.t.Helper()
	f.result = f.repo.commit(message)
	files, err := readFiles(context.Background(), repository.Git{Root: f.repo.root}, f.result)
	if err != nil {
		f.repo.t.Fatal(err)
	}
	f.receipt.WikiDigest = Digest(files)
	f.writeReceipt()
}

func (f *completionFixture) remove(name string) {
	f.repo.t.Helper()
	if err := os.Remove(filepath.Join(f.repo.root, filepath.FromSlash(name))); err != nil {
		f.repo.t.Fatal(err)
	}
}

// unrelatedCommit returns a commit with no parent and restores the branch culler.
func (f *completionFixture) unrelatedCommit() string {
	f.repo.t.Helper()
	f.repo.git("checkout", "--quiet", "--orphan", "unrelated")
	commit := f.repo.commit("Create an unrelated history")
	f.repo.git("checkout", "--quiet", "culler")
	return commit
}

// startEntry commits the index as the new start commit. The wiki commit is that start commit.
func (f *completionFixture) startEntry(arrange func()) {
	f.repo.t.Helper()
	arrange()
	f.repo.git("commit", "--quiet", "-m", "Accept a wiki entry")
	start := f.repo.git("rev-parse", "HEAD")
	f.input, f.source, f.result = start, start, start
	f.receipt.InputCommit, f.receipt.SourceCommit = start, start
	f.writeReceipt()
}

func (f *completionFixture) link(name string) {
	f.repo.t.Helper()
	if err := os.Symlink("../source.txt", filepath.Join(f.repo.root, filepath.FromSlash(name))); err != nil {
		f.repo.t.Fatal(err)
	}
	f.repo.git("add", "--all")
}

// submodule adds a submodule entry. The work tree holds an empty folder, as after a checkout.
func (f *completionFixture) submodule(name string) {
	f.repo.t.Helper()
	if err := os.Mkdir(filepath.Join(f.repo.root, filepath.FromSlash(name)), 0700); err != nil {
		f.repo.t.Fatal(err)
	}
	f.repo.git("add", "--all")
	f.repo.git("update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat("1", 40)+","+name)
}

// large writes a wiki file that brings the wiki above the read limit.
func (f *completionFixture) large(name string) {
	f.repo.t.Helper()
	filename := filepath.Join(f.repo.root, filepath.FromSlash(name))
	if err := os.WriteFile(filename, nil, 0644); err != nil {
		f.repo.t.Fatal(err)
	}
	if err := os.Truncate(filename, maxWikiBytes); err != nil {
		f.repo.t.Fatal(err)
	}
}

func TestCompletionAcceptsNavigationChangeAndPreservedBody(t *testing.T) {
	const document = "openwiki/reports/retention.md"
	const body = "# Retention result\n\nThe complete result of the Worker.\n"
	preserved := "task_id: 7\ntask_status: verified\nauthor: Worker\n---\n" + body
	prepare := func(t *testing.T, corrected string) *completionFixture {
		t.Helper()
		f := newCompletionFixture(t)
		// A merged task added the project document. No navigation page names it.
		f.repo.write(document, "---\ntitle: draft\ntype: Note\ndocument: true\ntags: [draft]\n"+preserved)
		start := f.repo.commit("Merge the task with the project document")
		f.input, f.receipt.InputCommit = start, start
		f.repo.write(document, corrected)
		f.source = f.repo.commit("Correct the display fields of the project document")
		f.receipt.SourceCommit = f.source
		f.repo.write("openwiki/index.md", "# Project wiki\n\n[Reports](reports/index.md)\n")
		f.repo.write("openwiki/reports/index.md", "# Reports\n\n[Task report](task.md)\n[Retention result](retention.md)\n")
		f.commitResult("Add the project document to the navigation pages")
		return f
	}

	corrected := "---\ntitle: Retention result\ntype: Research\ndocument: \"true\"\ntags:\n  - retention\n  - research\n" + preserved
	f := prepare(t, corrected)
	if err := f.validate(); err != nil {
		t.Fatalf("a navigation change with four corrected fields and a preserved body = %v, want accepted checks", err)
	}
	g := repository.Git{Root: f.repo.root}
	before, err := readFiles(context.Background(), g, f.input)
	if err != nil {
		t.Fatal(err)
	}
	after, err := readFiles(context.Background(), g, f.result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(before["openwiki/index.md"])+string(before["openwiki/reports/index.md"]), "retention.md") ||
		!strings.Contains(string(after["openwiki/reports/index.md"]), "(retention.md)") {
		t.Fatal("the fixture must add the project document to a navigation page in the wiki commit")
	}
	if string(after[document]) != corrected || !strings.HasSuffix(string(before[document]), preserved) {
		t.Fatalf("the wiki commit holds another project document:\n%s", after[document])
	}

	// The same wiki update with a changed protected field fails.
	f = prepare(t, strings.Replace(corrected, "task_status: verified", "task_status: merged", 1))
	want := "The wiki commit changes a protected part of a project document: " + document + "."
	if err := f.validate(); err == nil || err.Error() != want {
		t.Fatalf("a changed task status field = %v, want %q", err, want)
	}
}

// The Scribe can write the configuration of the repository that the check
// reads. A setting cannot hide a submodule entry that the wiki commit changes.
func TestCompletionChecksASubmoduleEntryOutsideTheWiki(t *testing.T) {
	f := newCompletionFixture(t)
	entry := func(commit, message string) string {
		f.repo.git("update-index", "--add", "--cacheinfo", "160000,"+strings.Repeat(commit, 40)+",vendor/dep")
		f.repo.git("commit", "--quiet", "-m", message)
		return f.repo.git("rev-parse", "HEAD")
	}
	start := entry("a", "Accept a submodule")
	f.input, f.source = start, start
	f.receipt.InputCommit, f.receipt.SourceCommit = start, start
	f.result = entry("b", "Move the submodule to another commit")
	f.writeReceipt()

	const want = "The wiki commit changes a path outside openwiki/: vendor/dep."
	for _, setting := range []string{"", "all"} {
		if setting != "" {
			f.repo.git("config", "diff.ignoreSubmodules", setting)
		}
		var failed *CheckError
		if err := f.validate(); !errors.As(err, &failed) || failed.Text != want {
			t.Fatalf("check result with diff.ignoreSubmodules=%q = %v, want the failed check %q", setting, err, want)
		}
	}
}

func TestCompletionCheckTexts(t *testing.T) {
	const task = "openwiki/reports/task.md"
	const retitled = "---\ntitle: Report of the task\ntype: Research\ndocument: true\n---\nComplete accepted report.\n"
	checks := []struct {
		name    string
		arrange func(t *testing.T, f *completionFixture)
		want    string
	}{
		{"wiki commit outside the history of the start commit", func(t *testing.T, f *completionFixture) {
			f.result = f.unrelatedCommit()
		}, "The history of the wiki commit omits the start commit."},
		{"source commit outside the history of the wiki commit", func(t *testing.T, f *completionFixture) {
			f.receipt.SourceCommit = f.unrelatedCommit()
			f.writeReceipt()
		}, "The source commit of the OpenWiki run is outside the history of the wiki commit."},
		{"path outside the wiki", func(t *testing.T, f *completionFixture) {
			f.repo.write("source.txt", "A source change of the Scribe.\n")
			f.commitResult("Change a source file")
		}, "The wiki commit changes a path outside openwiki/: source.txt."},
		{"wiki instructions", func(t *testing.T, f *completionFixture) {
			f.repo.write("openwiki/INSTRUCTIONS.md", "Another brief.\n")
			f.commitResult("Change the wiki instructions")
		}, "The wiki commit changes openwiki/INSTRUCTIONS.md."},
		{"Claim sidecar of a project document", func(t *testing.T, f *completionFixture) {
			f.repo.write(task, retitled)
			f.repo.write("openwiki/.claims/reports/task.json", "{\"claims\":[]}\n")
			f.commitResult("Add a Claim sidecar to the project document")
		}, "The wiki commit adds a Claim sidecar to a project document: " + task + "."},
		{"body of a project document", func(t *testing.T, f *completionFixture) {
			f.repo.write(task, "---\ntitle: Task report\ntype: Research\ndocument: true\n---\nAnother conclusion.\n")
			f.commitResult("Change the body of the project document")
		}, "The wiki commit changes a protected part of a project document: " + task + "."},
		{"removed project document", func(t *testing.T, f *completionFixture) {
			f.remove(task)
			f.commitResult("Remove the project document")
		}, "The wiki commit changes a protected part of a project document: " + task + "."},
		{"new project document", func(t *testing.T, f *completionFixture) {
			f.repo.write("openwiki/reports/new.md", "---\ntitle: New report\ntype: Research\ndocument: true\n---\nA report of the Scribe.\n")
			f.commitResult("Add a project document")
		}, "The wiki commit adds a project document: openwiki/reports/new.md."},
		{"generated page with the document mark", func(t *testing.T, f *completionFixture) {
			f.repo.write("openwiki/pages/flow.md", "---\ntitle: Flow\ntype: Component\ndocument: true\n---\nA generated explanation.\n")
			f.repo.write("openwiki/.claims/pages/flow.json", "{\"claims\":[]}\n")
			start := f.repo.commit("Hold a generated page with the document mark")
			f.input, f.receipt.InputCommit, f.receipt.SourceCommit = start, start, start
			f.repo.write("openwiki/pages/flow.md", "---\ntitle: Request flow\ntype: Component\ndocument: true\n---\nA generated explanation.\n")
			f.remove("openwiki/.claims/pages/flow.json")
			f.commitResult("Remove the Claim sidecar of the page")
		}, "The wiki commit turns a generated page into a project document: openwiki/pages/flow.md."},
		{"project document after the OpenWiki run", func(t *testing.T, f *completionFixture) {
			f.repo.write(task, retitled)
			f.commitResult("Correct the title after the OpenWiki run")
		}, "The wiki commit changes a project document after the OpenWiki run: " + task + "."},
		{"receipt above the size limit", func(t *testing.T, f *completionFixture) {
			body, err := os.ReadFile(f.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.path, append(body, strings.Repeat(" ", 16385)...), 0600); err != nil {
				t.Fatal(err)
			}
		}, "The OpenWiki completion receipt is invalid."},
		{"receipt with no JSON object", func(t *testing.T, f *completionFixture) {
			if err := os.WriteFile(f.path, []byte("complete\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "The OpenWiki completion receipt is invalid."},
		{"receipt of an incomplete run", func(t *testing.T, f *completionFixture) {
			f.receipt.Status = "incomplete"
			f.writeReceipt()
		}, "The OpenWiki completion receipt names another start commit, another OpenWiki version, or an incomplete run."},
		{"no index page", func(t *testing.T, f *completionFixture) {
			f.remove("openwiki/index.md")
			f.commitResult("Remove the index page")
		}, "The wiki commit lacks openwiki/index.md."},
		{"wiki files after the complete run", func(t *testing.T, f *completionFixture) {
			f.repo.write("openwiki/index.md", "# Project wiki\n\nA change after the complete run.\n")
			f.result = f.repo.commit("Change the index page after the receipt")
		}, "The wiki files of the wiki commit differ from the files of the complete OpenWiki run."},
		{"executable wiki file", func(t *testing.T, f *completionFixture) {
			if err := os.Chmod(filepath.Join(f.repo.root, "openwiki/index.md"), 0755); err != nil {
				t.Fatal(err)
			}
			f.commitResult("Make the index page executable")
		}, "The wiki commit holds a wiki file with a special file mode: openwiki/index.md."},
		{"wiki above the read limit", func(t *testing.T, f *completionFixture) {
			f.large("openwiki/large.md")
			f.result = f.repo.commit("Add a wiki file above the read limit")
		}, "The wiki of the wiki commit exceeds the read limit of 64 MiB."},
		{"wiki of the start commit above the read limit", func(t *testing.T, f *completionFixture) {
			f.large("openwiki/large.md")
			start := f.repo.commit("Accept a wiki above the read limit")
			f.input, f.receipt.InputCommit, f.receipt.SourceCommit = start, start, start
			f.remove("openwiki/large.md")
			f.commitResult("Remove the large wiki file")
		}, "The wiki of the start commit exceeds the read limit of 64 MiB."},
		{"incomplete page queue", func(t *testing.T, f *completionFixture) {
			f.repo.write("openwiki/.run.json", "{\"status\":\"incomplete\"}\n")
			f.commitResult("Keep the page queue")
		}, "The wiki commit holds the record of an incomplete OpenWiki page queue."},
		{"incomplete document checks", func(t *testing.T, f *completionFixture) {
			f.repo.write("openwiki/.culler-run.json", "{\"status\":\"incomplete\"}\n")
			f.commitResult("Keep the document checks")
		}, "The wiki commit holds the record of incomplete document checks."},
		{"update record of an incomplete run", func(t *testing.T, f *completionFixture) {
			f.repo.write("openwiki/.last-update.json", "{\"status\":\"running\"}\n")
			f.commitResult("Keep the update record of an incomplete run")
		}, "The wiki commit holds no record of a complete OpenWiki update."},
		{"no update record", func(t *testing.T, f *completionFixture) {
			f.remove("openwiki/.last-update.json")
			f.commitResult("Remove the update record")
		}, "The wiki commit holds no record of a complete OpenWiki update."},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			f := newCompletionFixture(t)
			check.arrange(t, f)
			var failed *CheckError
			if err := f.validate(); !errors.As(err, &failed) || failed.Text != check.want {
				t.Fatalf("check result = %v, want the failed check %q", err, check.want)
			}
		})
	}

	const unreadable = "The OpenWiki completion receipt is not a readable regular file."
	f := newCompletionFixture(t)
	folder := t.TempDir()
	absent, link := filepath.Join(folder, "absent.json"), filepath.Join(folder, "link.json")
	if err := os.Symlink(f.path, link); err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []struct{ name, result, path string }{
		{"absent receipt", f.result, absent},
		{"folder", f.result, folder},
		{"symbolic link", f.result, link},
		// An unreadable receipt gives no check result, also for a wiki commit that fails a check.
		{"absent receipt of a wiki commit outside the history", f.unrelatedCommit(), absent},
	} {
		t.Run(receipt.name, func(t *testing.T) {
			f.result, f.path = receipt.result, receipt.path
			var refused *ReceiptError
			if err := f.validate(); !errors.As(err, &refused) || refused.Text != unreadable {
				t.Fatalf("check result = %v, want the receipt error %q", err, unreadable)
			}
		})
	}
}

// An entry under openwiki/ that is no regular file fails a check. The text names the commit that holds the entry.
func TestCompletionCheckTextsOfAnEntryThatIsNoRegularFile(t *testing.T) {
	checks := []struct {
		name    string
		arrange func(t *testing.T, f *completionFixture)
		want    string
	}{
		{"symbolic link in the start commit", func(t *testing.T, f *completionFixture) {
			f.startEntry(func() { f.link("openwiki/link.md") })
		}, "The start commit holds a symbolic link under openwiki/: openwiki/link.md."},
		{"submodule in the start commit", func(t *testing.T, f *completionFixture) {
			f.startEntry(func() { f.submodule("openwiki/vendor") })
		}, "The start commit holds a submodule under openwiki/: openwiki/vendor."},
		{"submodule in the start commit that the wiki commit keeps", func(t *testing.T, f *completionFixture) {
			f.startEntry(func() { f.submodule("openwiki/vendor") })
			f.repo.write("openwiki/index.md", "# Project wiki\n\nA change.\n")
			f.result = f.repo.commit("Update the Project wiki")
			if got := f.repo.git("ls-tree", f.result, "--", "openwiki/vendor"); !strings.HasPrefix(got, "160000 ") {
				t.Fatalf("the wiki commit lost the submodule: %q", got)
			}
		}, "The start commit holds a submodule under openwiki/: openwiki/vendor."},
		{"symbolic link in the start commit that the wiki commit removes", func(t *testing.T, f *completionFixture) {
			f.startEntry(func() { f.link("openwiki/link.md") })
			f.remove("openwiki/link.md")
			f.commitResult("Remove the symbolic link")
		}, "The start commit holds a symbolic link under openwiki/: openwiki/link.md."},
		{"path with a backslash in the start commit", func(t *testing.T, f *completionFixture) {
			f.startEntry(func() {
				f.repo.write("openwiki/notes\\draft.md", "A page.\n")
				f.repo.git("add", "--all")
			})
		}, "The start commit holds a path under openwiki/ with a backslash or a line break."},
		{"symbolic link in the source commit of the OpenWiki run", func(t *testing.T, f *completionFixture) {
			f.link("openwiki/link.md")
			f.source = f.repo.commit("Add a symbolic link before the OpenWiki run")
			f.receipt.SourceCommit = f.source
			f.remove("openwiki/link.md")
			f.commitResult("Remove the symbolic link")
		}, "The source commit of the OpenWiki run holds a symbolic link under openwiki/: openwiki/link.md."},
		{"symbolic link in the wiki commit", func(t *testing.T, f *completionFixture) {
			f.link("openwiki/link.md")
			f.result = f.repo.commit("Add a symbolic link")
		}, "The wiki commit holds a symbolic link under openwiki/: openwiki/link.md."},
		{"path with a line break in the wiki commit", func(t *testing.T, f *completionFixture) {
			f.repo.write("openwiki/line\nbreak.md", "A page.\n")
			f.result = f.repo.commit("Add a page with a line break in its name")
		}, "The wiki commit holds a path under openwiki/ with a backslash or a line break."},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			f := newCompletionFixture(t)
			check.arrange(t, f)
			var failed *CheckError
			if err := f.validate(); !errors.As(err, &failed) || failed.Text != check.want {
				t.Fatalf("check result = %v, want the failed check %q", err, check.want)
			}
		})
	}
}

// A Git process that fails gives no check result. A later check of the same wiki commit can succeed.
func TestCompletionGivesNoCheckResultAfterAGitFailure(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{"1", "2", "3"} {
		t.Run("ancestor test "+call, func(t *testing.T) {
			f := newCompletionFixture(t)
			if err := f.validate(); err != nil {
				t.Fatalf("the fixture fails a check: %v", err)
			}
			// The stub program fails one ancestor test and passes each other command to Git.
			folder := t.TempDir()
			count := filepath.Join(folder, "count")
			body := "#!/bin/sh\nfor argument in \"$@\"; do\n  if [ \"$argument\" = merge-base ]; then\n" +
				"    echo x >> '" + count + "'\n" +
				"    if [ \"$(($(wc -l < '" + count + "')))\" = " + call + " ]; then echo 'fatal: the process failed' >&2; exit 128; fi\n" +
				"  fi\ndone\nexec '" + real + "' \"$@\"\n"
			if err := os.WriteFile(filepath.Join(folder, "git"), []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", folder+string(os.PathListSeparator)+os.Getenv("PATH"))
			err := f.validate()
			var failed *CheckError
			var receipt *ReceiptError
			if err == nil || errors.As(err, &failed) || errors.As(err, &receipt) || !strings.Contains(err.Error(), "git merge-base") {
				t.Fatalf("check result = %v, want the error of the Git process", err)
			}
			if err := f.validate(); err != nil {
				t.Fatalf("second check = %v, want accepted checks", err)
			}
		})
	}
}

// The scope check of a merge candidate names the merge candidate and its destination commit.
func TestMergeCandidateCheckTexts(t *testing.T) {
	const task = "openwiki/reports/task.md"
	checks := []struct {
		name    string
		arrange func(f *completionFixture)
		want    string
	}{
		{"wiki change", func(f *completionFixture) {
			f.repo.write("openwiki/index.md", "# Project wiki\n\nA change.\n")
		}, ""},
		{"path outside the wiki", func(f *completionFixture) {
			f.repo.write("source.txt", "Another application source.\n")
		}, "The merge candidate changes a path outside openwiki/: source.txt."},
		{"body of a project document", func(f *completionFixture) {
			f.repo.write(task, "---\ntitle: Task report\ntype: Research\ndocument: true\n---\nAnother conclusion.\n")
		}, "The merge candidate changes a protected part of a project document: " + task + "."},
		{"executable wiki file", func(f *completionFixture) {
			if err := os.Chmod(filepath.Join(f.repo.root, "openwiki/index.md"), 0755); err != nil {
				f.repo.t.Fatal(err)
			}
		}, "The merge candidate holds a wiki file with a special file mode: openwiki/index.md."},
		{"symbolic link in the merge candidate", func(f *completionFixture) {
			f.link("openwiki/link.md")
		}, "The merge candidate holds a symbolic link under openwiki/: openwiki/link.md."},
		{"symbolic link in the destination commit", func(f *completionFixture) {
			f.link("openwiki/link.md")
			f.result = f.repo.commit("Accept a symbolic link")
			f.repo.write("openwiki/index.md", "# Project wiki\n\nA change.\n")
		}, "The destination commit holds a symbolic link under openwiki/: openwiki/link.md."},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			f := newCompletionFixture(t)
			check.arrange(f)
			destination, candidate := f.result, f.repo.commit("Merge the wiki commit")
			err := ValidateMergeCandidate(context.Background(), repository.Git{Root: f.repo.root}, destination, candidate)
			var failed *CheckError
			if check.want == "" {
				if err != nil {
					t.Fatalf("check result = %v, want accepted checks", err)
				}
			} else if !errors.As(err, &failed) || failed.Text != check.want {
				t.Fatalf("check result = %v, want the failed check %q", err, check.want)
			}
		})
	}
}
