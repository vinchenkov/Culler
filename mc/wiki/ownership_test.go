package wiki

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/repository"
)

func TestTaskCommitPreservesWikiEntries(t *testing.T) {
	for name, change := range map[string]func(wikiRepository){
		"add":    func(r wikiRepository) { r.write("openwiki/report.md", "Original research.\n") },
		"edit":   func(r wikiRepository) { r.write("openwiki/index.md", "A replacement index.\n") },
		"delete": func(r wikiRepository) { r.git("rm", "openwiki/index.md") },
		"rename": func(r wikiRepository) { r.git("mv", "openwiki/index.md", "docs.md") },
		"mode": func(r wikiRepository) {
			if err := os.Chmod(filepath.Join(r.root, "openwiki/index.md"), 0755); err != nil {
				r.t.Fatal(err)
			}
		},
		"replace root": func(r wikiRepository) {
			r.git("rm", "-r", "openwiki")
			r.write("openwiki", "A replacement file.\n")
		},
		"symbolic link": func(r wikiRepository) {
			r.git("rm", "-r", "openwiki")
			if err := os.Symlink("docs", filepath.Join(r.root, "openwiki")); err != nil {
				r.t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := newWikiRepository(t)
			r.write("openwiki/index.md", "The accepted wiki.\n")
			input := r.commit("Accept the wiki")
			change(r)
			result := r.commit("Change the wiki")
			err := ValidateTaskCommit(context.Background(), repository.Git{Root: r.root}, input, input, result)
			if err == nil || !strings.Contains(err.Error(), "Only the Scribe") {
				t.Fatalf("the task wiki change passed: %v", err)
			}
		})
	}
}

func TestTaskDocumentsRemainIndependentOfWiki(t *testing.T) {
	for _, withWiki := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent wiki", true: "present wiki"}[withWiki], func(t *testing.T) {
			r := newWikiRepository(t)
			r.write("source.txt", "The source.\n")
			if withWiki {
				r.write("openwiki/index.md", "The accepted wiki.\n")
			}
			input := r.commit("Accept the source")
			r.write("docs/research/report.html", "<html><body>Complete research.</body></html>\n")
			r.write("docs/research/data.csv", "sample,value\na,42\n")
			r.write("docs/decision.md", "The accepted decision.\n")
			r.write("openwiki-notes.md", "A file outside the wiki.\n")
			result := r.commit("Add original project documents")
			g := repository.Git{Root: r.root}
			if err := ValidateTaskCommit(context.Background(), g, input, input, result); err != nil {
				t.Fatal(err)
			}
			r.write("openwiki/new.md", "A task-authored wiki page.\n")
			changed := r.commit("Add a wiki page")
			if err := ValidateTaskCommit(context.Background(), g, result, input, changed); err == nil {
				t.Fatal("the task created a wiki page")
			}
		})
	}
}

func TestTaskCommitRefusesAFileAtAnAbsentWikiRoot(t *testing.T) {
	r := newWikiRepository(t)
	r.write("source.txt", "The source.\n")
	input := r.commit("Accept the source without a wiki")
	r.write("openwiki", "A file at the reserved wiki path.\n")
	result := r.commit("Create a file at the wiki root")
	if err := ValidateTaskCommit(context.Background(), repository.Git{Root: r.root}, input, input, result); err == nil {
		t.Fatal("the task created a file at the reserved wiki root")
	}
}

func TestTaskMergePreservesAcceptedWiki(t *testing.T) {
	r := newWikiRepository(t)
	r.write("openwiki/index.md", "The first wiki.\n")
	input := r.commit("Accept the first wiki")
	r.git("checkout", "-qb", "task")
	r.write("docs/research.md", "Original research.\n")
	submitted := r.commit("Add the research")
	r.git("checkout", "-q", "culler")
	r.write("openwiki/index.md", "The current wiki.\n")
	destination := r.commit("Update the accepted wiki")
	r.git("checkout", "-q", "task")
	g := repository.Git{Root: r.root}
	ctx := context.Background()
	if err := ValidateTaskCommit(ctx, g, input, destination, submitted); err != nil {
		t.Fatal(err)
	}
	r.write("openwiki/index.md", "The current wiki.\n")
	copy := r.commit("Copy the destination wiki without its commit")
	if err := ValidateTaskCommit(ctx, g, input, destination, copy); err == nil {
		t.Fatal("the task changed its wiki without a destination merge")
	}
	r.git("reset", "--hard", submitted)
	r.git("merge", "--no-edit", "culler")
	merged := r.git("rev-parse", "HEAD")
	if err := ValidateTaskCommit(ctx, g, input, destination, merged); err != nil {
		t.Fatal(err)
	}
	r.write("openwiki/index.md", "The first wiki.\n")
	reverted := r.commit("Revert the accepted wiki")
	if err := ValidateTaskCommit(ctx, g, input, destination, reverted); err == nil {
		t.Fatal("the task reverted the accepted wiki after a merge")
	}
}
