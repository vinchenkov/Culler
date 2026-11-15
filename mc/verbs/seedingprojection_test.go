package verbs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMaterializeSeedingCommittedSourceExcludesLiveCheckoutAndGitControls(t *testing.T) {
	source, committed, format := buildSourceRepo(t)
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("dirty checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcGit(t, source, "add", "README.md")
	if err := os.WriteFile(filepath.Join(source, "untracked.txt"), []byte("never project me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	projection := filepath.Join(t.TempDir(), "projection")
	if err := os.Mkdir(projection, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := MaterializeSeedingCommittedSource(source, projection, "HEAD", format)
	if err != nil {
		t.Fatalf("materialize committed source: %v", err)
	}
	if got != committed {
		t.Fatalf("projected commit = %q, want %q", got, committed)
	}
	readme, err := os.ReadFile(filepath.Join(projection, "README.md"))
	if err != nil || string(readme) == "dirty checkout\n" {
		t.Fatalf("projection used live checkout bytes: body=%q err=%v", readme, err)
	}
	if _, err := os.Lstat(filepath.Join(projection, "untracked.txt")); !os.IsNotExist(err) {
		t.Fatalf("projection admitted an untracked file: %v", err)
	}
	gitInfo, err := os.Lstat(filepath.Join(projection, ".git"))
	if err != nil || !gitInfo.IsDir() {
		t.Fatalf("projection .git cover = %v, %v; want empty directory", gitInfo, err)
	}
	entries, err := os.ReadDir(filepath.Join(projection, ".git"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("projection .git cover is not empty: entries=%v err=%v", entries, err)
	}
}

// A committed tree that carries the artifact root at its top level is refused.
// The projection would otherwise place Culler-owned state under a Git cover.
func TestMaterializeSeedingCommittedSourceRefusesReservedArtifactRoot(t *testing.T) {
	source, _, format := buildSourceRepo(t)
	if err := os.MkdirAll(filepath.Join(source, ".mc-artifacts"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(source, ".mc-artifacts", "packet.html"), "<html>\n")
	srcGit(t, source, "add", "-f", ".mc-artifacts")
	srcGit(t, source, "commit", "-qm", "commit the artifact root")
	projection := filepath.Join(t.TempDir(), "projection")
	if err := os.Mkdir(projection, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := MaterializeSeedingCommittedSource(source, projection, "HEAD", format)
	if err == nil || !strings.Contains(err.Error(), `reserved ".mc-artifacts"`) {
		t.Fatalf("committed artifact root = %v, want a reserved component refusal", err)
	}
	entries, readErr := os.ReadDir(projection)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("refused projection wrote entries: %v %v", entries, readErr)
	}
}

func TestMaterializeSeedingCommittedSourceRefusesResidue(t *testing.T) {
	source, _, format := buildSourceRepo(t)
	projection := t.TempDir()
	if err := os.WriteFile(filepath.Join(projection, "residue"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := MaterializeSeedingCommittedSource(source, projection, "HEAD", format); err == nil {
		t.Fatal("seeding projection adopted an existing nonempty root")
	}
}

// The Strategist projection reads the culler branch, so the branch that the
// operator has checked out does not change what the Strategist sees.
func TestMaterializeSeedingCommittedSourceReadsTheCullerBranch(t *testing.T) {
	source, committed, format := buildSourceRepo(t)
	srcGit(t, source, "branch", "culler", committed)
	srcGit(t, source, "checkout", "--quiet", "-b", "operator")
	writeFile(t, filepath.Join(source, "README.md"), "operator branch\n")
	srcGit(t, source, "commit", "-qam", "operator work")
	projection := filepath.Join(t.TempDir(), "projection")
	if err := os.Mkdir(projection, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := MaterializeSeedingCommittedSource(source, projection, "refs/heads/culler", format)
	if err != nil || got != committed {
		t.Fatalf("projected commit = %q %v, want the culler commit %q", got, err, committed)
	}
	readme, err := os.ReadFile(filepath.Join(projection, "README.md"))
	if err != nil || string(readme) != "hello world\nsecond\n" {
		t.Fatalf("projection used the checked-out branch: body=%q err=%v", readme, err)
	}
}
