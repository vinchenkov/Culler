package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func gitTest(t *testing.T, g Git, args ...string) string {
	t.Helper()
	out, err := g.output(context.Background(), args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func fixture(t *testing.T) (Git, string) {
	t.Helper()
	g := Git{Root: t.TempDir()}
	gitTest(t, g, "init", "--quiet", "-b", "culler")
	gitTest(t, g, "config", "user.name", "Test Author")
	gitTest(t, g, "config", "user.email", "test@example.invalid")
	writeTest(t, g.Root, "settings.txt", "settings = { limit: 10, label: old }\n")
	gitTest(t, g, "add", ".")
	gitTest(t, g, "commit", "--quiet", "-m", "Create the settings")
	return g, gitTest(t, g, "rev-parse", "HEAD")
}

func writeTest(t *testing.T, root, path, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func runTest(t *testing.T, source Git, commits ...string) Git {
	t.Helper()
	g, err := Isolated(context.Background(), source.Root, filepath.Join(t.TempDir(), "repo"), commits...)
	if err != nil {
		t.Fatal(err)
	}
	gitTest(t, g, "config", "user.name", "Test Author")
	gitTest(t, g, "config", "user.email", "test@example.invalid")
	return g
}

func TestExactVersionSurvivesMainMovementAndCleanup(t *testing.T) {
	ctx := context.Background()
	source, initial := fixture(t)
	worker := runTest(t, source, initial)
	writeTest(t, worker.Root, "worker.txt", "worker contribution\n")
	gitTest(t, worker, "add", ".")
	gitTest(t, worker, "commit", "--quiet", "-m", "Add the contribution")
	version := gitTest(t, worker, "rev-parse", "HEAD")
	ref := "refs/mc/tasks/42/versions/1"
	if err := source.Retain(ctx, worker.Root, version, ref); err != nil {
		t.Fatal(err)
	}
	if gitTest(t, source, "rev-parse", "culler") != initial {
		t.Fatal("retention advanced culler")
	}
	if err := os.RemoveAll(worker.Root); err != nil {
		t.Fatal(err)
	}
	writeTest(t, source.Root, "main.txt", "another accepted contribution\n")
	gitTest(t, source, "add", ".")
	gitTest(t, source, "commit", "--quiet", "-m", "Advance culler")
	main := gitTest(t, source, "rev-parse", "HEAD")
	gitTest(t, source, "gc", "--prune=now")
	verifier := runTest(t, source, version)
	if gitTest(t, verifier, "rev-parse", "HEAD") != version {
		t.Fatal("the Verifier received another version")
	}
	if _, err := os.Stat(filepath.Join(verifier.Root, "main.txt")); !os.IsNotExist(err) {
		t.Fatal("culler entered the submitted version")
	}
	preparation := runTest(t, source, main, version)
	candidate, err := Prepare(ctx, preparation, version, "culler", main, "Test Author", "test@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"worker.txt", "main.txt"} {
		if _, err := os.Stat(filepath.Join(preparation.Root, path)); err != nil {
			t.Fatalf("the candidate lost %s: %v", path, err)
		}
	}
	if candidate.Commit == version || candidate.DestinationCommit != main {
		t.Fatal("the candidate lost its merge inputs")
	}
	if err := source.Retain(ctx, preparation.Root, candidate.Commit, "refs/mc/tasks/42/candidates/1"); err != nil {
		t.Fatal(err)
	}
	if gitTest(t, source, "rev-parse", "culler") != main {
		t.Fatal("preparation advanced culler")
	}
	app := Application{Authorization: "run-apply-1", Candidate: candidate}
	crash := errors.New("the process stopped after the Git update")
	_, err = apply(ctx, source.Root, app, func(at string) error {
		if at == "after-update" {
			return crash
		}
		return nil
	})
	if !errors.Is(err, crash) {
		t.Fatalf("the test missed its crash boundary: %v", err)
	}
	applied, err := Apply(ctx, source.Root, app)
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Recovered || gitTest(t, source, "rev-parse", "culler") != candidate.Commit {
		t.Fatal("recovery lost the accepted commit")
	}
	if got := gitTest(t, source, "status", "--porcelain"); got != "" {
		t.Fatalf("the checkout diverges: %s", got)
	}
}

func TestConflictPreservesBothInputs(t *testing.T) {
	ctx := context.Background()
	source, initial := fixture(t)
	worker := runTest(t, source, initial)
	writeTest(t, worker.Root, "settings.txt", "settings = { limit: 20, label: old }\n")
	gitTest(t, worker, "add", ".")
	gitTest(t, worker, "commit", "--quiet", "-m", "Change the limit")
	version := gitTest(t, worker, "rev-parse", "HEAD")
	if err := source.Retain(ctx, worker.Root, version, "refs/mc/tasks/2/versions/1"); err != nil {
		t.Fatal(err)
	}
	writeTest(t, source.Root, "settings.txt", "settings = { limit: 10, label: new }\n")
	gitTest(t, source, "add", ".")
	gitTest(t, source, "commit", "--quiet", "-m", "Change the label")
	main := gitTest(t, source, "rev-parse", "HEAD")
	preparation := runTest(t, source, main, version)
	candidate, err := Prepare(ctx, preparation, version, "culler", main, "Test Author", "test@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Status != "conflict" || strings.Join(candidate.ConflictPaths, ",") != "settings.txt" || candidate.Validate() == nil {
		t.Fatalf("the test missed the conflict: %+v", candidate)
	}
	if gitTest(t, source, "rev-parse", "culler") != main {
		t.Fatal("the conflict changed culler")
	}
	writeTest(t, preparation.Root, "settings.txt", "settings = { limit: 20, label: new }\n")
	gitTest(t, preparation, "add", ".")
	gitTest(t, preparation, "commit", "--quiet", "-m", "Preserve both settings")
	repaired := gitTest(t, preparation, "rev-parse", "HEAD")
	candidate, err = Prepare(ctx, preparation, repaired, "culler", main, "Test Author", "test@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatal(err)
	}
}

// mergeFixture returns a Project repository and a retained candidate that
// changes settings.txt and adds new.txt.
func mergeFixture(t *testing.T) (Git, string, Candidate) {
	t.Helper()
	ctx := context.Background()
	source, initial := fixture(t)
	writeTest(t, source.Root, "notes.txt", "operator notes\n")
	gitTest(t, source, "add", ".")
	gitTest(t, source, "commit", "--quiet", "-m", "Add the notes")
	initial = gitTest(t, source, "rev-parse", "HEAD")
	worker := runTest(t, source, initial)
	writeTest(t, worker.Root, "settings.txt", "settings = { limit: 20, label: old }\n")
	writeTest(t, worker.Root, "new.txt", "the contribution\n")
	gitTest(t, worker, "add", ".")
	gitTest(t, worker, "commit", "--quiet", "-m", "Add the contribution")
	version := gitTest(t, worker, "rev-parse", "HEAD")
	candidate, err := Prepare(ctx, worker, version, "culler", initial, "Test Author", "test@example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Retain(ctx, worker.Root, candidate.Commit, "refs/mc/tasks/1/candidates/1"); err != nil {
		t.Fatal(err)
	}
	return source, initial, candidate
}

func TestApplyRefusesDestinationRaceAndLocalChangesToTaskPaths(t *testing.T) {
	ctx := context.Background()
	for _, obstruction := range []string{"destination", "edit", "staged", "untracked", "ignored", "two", "merge", "rebase", "lock"} {
		t.Run(obstruction, func(t *testing.T) {
			source, initial, candidate := mergeFixture(t)
			var boundary func(string) error
			want := []string{"Commit, stash, or discard each of these changes."}
			switch obstruction {
			case "destination":
				boundary = func(at string) error {
					if at == "before-update" {
						gitTest(t, source, "update-ref", "refs/heads/culler", candidate.SubmittedCommit, initial)
					}
					return nil
				}
			case "edit":
				writeTest(t, source.Root, "settings.txt", "operator edits\n")
				want = append(want, "prevent the merge into culler: settings.txt.")
			case "staged":
				writeTest(t, source.Root, "settings.txt", "operator edits\n")
				gitTest(t, source, "add", "settings.txt")
				want = append(want, ": settings.txt.")
			case "untracked":
				writeTest(t, source.Root, "new.txt", "operator file\n")
				want = append(want, ": new.txt.")
			case "ignored":
				writeTest(t, source.Root, ".git/info/exclude", "new.txt\n")
				writeTest(t, source.Root, "new.txt", "operator file\n")
				want = append(want, ": new.txt.")
			case "two":
				writeTest(t, source.Root, "settings.txt", "operator edits\n")
				writeTest(t, source.Root, "new.txt", "operator file\n")
				want = append(want, ": new.txt, settings.txt.")
			case "merge":
				writeTest(t, source.Root, ".git/MERGE_HEAD", initial+"\n")
				want = []string{"A Git merge is in progress in the operator checkout. Finish or stop the merge."}
			case "rebase":
				if err := os.Mkdir(filepath.Join(source.Root, ".git", "rebase-merge"), 0700); err != nil {
					t.Fatal(err)
				}
				gitTest(t, source, "checkout", "--quiet", "--detach")
				want = []string{"A Git rebase is in progress in the operator checkout."}
			case "lock":
				writeTest(t, source.Root, ".git/index.lock", "")
				want = []string{"The operator checkout has the file .git/index.lock.", "When no Git command runs, remove the file."}
			}
			before := map[string]string{}
			for _, path := range []string{"settings.txt", "new.txt", ".git/index"} {
				body, _ := os.ReadFile(filepath.Join(source.Root, path))
				before[path] = string(body)
			}
			_, err := apply(ctx, source.Root, Application{Authorization: "test-apply", Candidate: candidate}, boundary)
			if err == nil {
				t.Fatal("the obstruction permitted an update")
			}
			if obstruction == "destination" {
				if !errors.Is(err, ErrDestinationChanged) {
					t.Fatal(err)
				}
				return
			}
			var refusal *CheckoutRefusal
			if !errors.As(err, &refusal) || !errors.Is(err, ErrOperatorCheckout) {
				t.Fatalf("the refusal = %v", err)
			}
			for _, text := range want {
				if !strings.Contains(refusal.Text, text) {
					t.Fatalf("the refusal %q lacks %q", refusal.Text, text)
				}
			}
			if strings.Contains(refusal.Text, "git ") {
				t.Fatalf("the refusal shows Git command text: %q", refusal.Text)
			}
			if gitTest(t, source, "rev-parse", "culler") != initial {
				t.Fatal("an obstruction changed culler")
			}
			for path, body := range before {
				if after, _ := os.ReadFile(filepath.Join(source.Root, path)); string(after) != body {
					t.Fatalf("the refused merge changed %s", path)
				}
			}
			if obstruction != "lock" {
				if _, err := os.Stat(filepath.Join(source.Root, ".git", "index.lock")); !os.IsNotExist(err) {
					t.Fatal("the refused merge kept the index lock")
				}
			}
		})
	}
}

func TestApplyPreservesLocalChangesOnOtherPaths(t *testing.T) {
	ctx := context.Background()
	source, _, candidate := mergeFixture(t)
	writeTest(t, source.Root, "notes.txt", "unstaged operator notes\n")
	writeTest(t, source.Root, "staged.txt", "a staged operator file\n")
	gitTest(t, source, "add", "staged.txt")
	writeTest(t, source.Root, "draft.txt", "an untracked operator file\n")
	if err := Preflight(ctx, source.Root, candidate); err != nil {
		t.Fatalf("unrelated local changes stopped the preflight: %v", err)
	}
	if _, err := Apply(ctx, source.Root, Application{Authorization: "unrelated-changes", Candidate: candidate}); err != nil {
		t.Fatalf("unrelated local changes stopped the merge: %v", err)
	}
	if gitTest(t, source, "rev-parse", "culler") != candidate.Commit {
		t.Fatal("the merge did not advance culler")
	}
	status := gitTest(t, source, "status", "--porcelain")
	if status != "M notes.txt\nA  staged.txt\n?? draft.txt" {
		t.Fatalf("the merge changed the local changes: %q", status)
	}
	for path, body := range map[string]string{
		"settings.txt": "settings = { limit: 20, label: old }\n", "new.txt": "the contribution\n",
		"notes.txt": "unstaged operator notes\n", "draft.txt": "an untracked operator file\n",
	} {
		if got, _ := os.ReadFile(filepath.Join(source.Root, path)); string(got) != body {
			t.Fatalf("%s = %q, want %q", path, got, body)
		}
	}
}

func TestPreflightNamesEachRefusedPathAndChangesNoFile(t *testing.T) {
	ctx := context.Background()
	source, initial, candidate := mergeFixture(t)
	writeTest(t, source.Root, "settings.txt", "operator edits\n")
	writeTest(t, source.Root, "new.txt", "operator file\n")
	writeTest(t, source.Root, "notes.txt", "unstaged operator notes\n")
	snapshot := func() map[string]string {
		files := map[string]string{}
		if err := filepath.WalkDir(source.Root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			body, err := os.ReadFile(path)
			files[path] = string(body)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return files
	}
	before := snapshot()
	err := Preflight(ctx, source.Root, candidate)
	var refusal *CheckoutRefusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Text, "prevent the merge into culler: new.txt, settings.txt.") {
		t.Fatalf("the preflight refusal = %v", err)
	}
	after := snapshot()
	if len(after) != len(before) {
		t.Fatalf("the preflight changed the file set: %d files, then %d files", len(before), len(after))
	}
	for path, body := range before {
		if after[path] != body {
			t.Fatalf("the preflight changed %s", path)
		}
	}
	if gitTest(t, source, "rev-parse", "culler") != initial {
		t.Fatal("the preflight changed culler")
	}
}

func TestApplicationRecoversEachDurableBoundary(t *testing.T) {
	for _, boundary := range []string{"after-journal", "after-checkout", "after-reference", "after-receipt", "after-update"} {
		t.Run(boundary, func(t *testing.T) {
			source, _, c := mergeFixture(t)
			writeTest(t, source.Root, "notes.txt", "unstaged operator notes\n")
			writeTest(t, source.Root, "staged.txt", "a staged operator file\n")
			gitTest(t, source, "add", "staged.txt")
			writeTest(t, source.Root, "draft.txt", "an untracked operator file\n")
			a := Application{Authorization: "durable-boundary", Candidate: c}
			reached := false
			injected := errors.New("the process stopped at its durable boundary")
			_, err := apply(context.Background(), source.Root, a, func(at string) error {
				if at == boundary {
					reached = true
					return injected
				}
				return nil
			})
			if !reached || !errors.Is(err, injected) {
				t.Fatalf("the test missed %s: %v", boundary, err)
			}
			result, err := Apply(context.Background(), source.Root, a)
			if err != nil || result.CleanupError != "" {
				t.Fatalf("recovery failed: %+v, %v", result, err)
			}
			if gitTest(t, source, "rev-parse", "HEAD") != c.Commit {
				t.Fatal("recovery did not advance culler")
			}
			if status := gitTest(t, source, "status", "--porcelain"); status != "M notes.txt\nA  staged.txt\n?? draft.txt" {
				t.Fatalf("recovery changed the local changes: %q", status)
			}
			repeated, err := Apply(context.Background(), source.Root, a)
			if err != nil || !repeated.Recovered || repeated.Commit != result.Commit {
				t.Fatal("the repeated report changed the original result")
			}
		})
	}
}

func TestInterruptedMergeRefusesAnEditOfATaskPath(t *testing.T) {
	ctx := context.Background()
	source, initial, c := mergeFixture(t)
	a := Application{Authorization: "interrupted-edit", Candidate: c}
	injected := errors.New("the process stopped after the journal")
	if _, err := apply(ctx, source.Root, a, func(at string) error {
		if at == "after-journal" {
			return injected
		}
		return nil
	}); !errors.Is(err, injected) {
		t.Fatal(err)
	}
	writeTest(t, source.Root, "settings.txt", "operator edits\n")
	_, err := Apply(ctx, source.Root, a)
	var refusal *CheckoutRefusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Text, "An interrupted merge into culler found local changes: settings.txt.") {
		t.Fatalf("the recovery refusal = %v", err)
	}
	if gitTest(t, source, "rev-parse", "culler") != initial {
		t.Fatal("the refused recovery changed culler")
	}
	if body, _ := os.ReadFile(filepath.Join(source.Root, "settings.txt")); string(body) != "operator edits\n" {
		t.Fatal("the refused recovery changed the operator edit")
	}
}

func TestIndependentRepositoriesHaveNoSharedObjectFiles(t *testing.T) {
	source, initial := fixture(t)
	run := runTest(t, source, initial)
	originals := []os.FileInfo{}
	if err := filepath.WalkDir(filepath.Join(source.Root, ".git", "objects"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err == nil {
			originals = append(originals, info)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	count := 0
	if err := filepath.WalkDir(filepath.Join(run.Root, ".git", "objects"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		count++
		for _, original := range originals {
			if os.SameFile(original, info) {
				t.Fatalf("the run shares a Git object file: %s", path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("the isolation test examined zero object files")
	}
	gitTest(t, run, "branch", "local-only", initial)
	if _, err := source.Commit(context.Background(), "refs/heads/local-only"); err == nil {
		t.Fatal("a run changed an accepted repository reference")
	}
}
