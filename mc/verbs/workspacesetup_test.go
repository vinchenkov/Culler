package verbs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceVisionPreservesExternalEdits(t *testing.T) {
	root := t.TempDir()
	info, _ := os.Stat(root)
	identity := workspaceDirectoryIdentity(info, true)
	before, err := ReadWorkspaceVision(root)
	if err != nil || before.Revision != "absent" {
		t.Fatalf("initial vision: %+v, %v", before, err)
	}
	saved, err := SaveWorkspaceVision(root, before.Revision, "Help people understand their expenses.", identity)
	if err != nil || !saved.Check.Ready {
		t.Fatalf("saved vision: %+v, %v", saved, err)
	}
	outside := "The operator changed this goal in an editor."
	if err := os.WriteFile(filepath.Join(root, "VISION.md"), []byte(outside), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveWorkspaceVision(root, saved.Revision, "A stale browser edit.", identity); err == nil {
		t.Fatal("a stale save replaced an external edit")
	}
	got, err := ReadWorkspaceVision(root)
	if err != nil || got.Content != outside {
		t.Fatalf("external edit: %+v, %v", got, err)
	}
	repeated, err := SaveWorkspaceVision(root, "old-revision", outside, identity)
	if err != nil || repeated.Content != outside {
		t.Fatal("an exact save retry failed")
	}
}

func TestWorkspaceVisionRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	info, _ := os.Stat(root)
	identity := workspaceDirectoryIdentity(info, true)
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("private text"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "VISION.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadWorkspaceVision(root); err == nil {
		t.Fatal("the vision read followed a symbolic link")
	}
	if _, err := SaveWorkspaceVision(root, "absent", "Overwrite", identity); err == nil {
		t.Fatal("the vision save followed a symbolic link")
	}
	body, _ := os.ReadFile(outside)
	if string(body) != "private text" {
		t.Fatal("the external file changed")
	}
}

func TestWorkspacePreviewDoesNotCreateProject(t *testing.T) {
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	path := filepath.Join(parent, "new-project")
	preview, err := PreviewWorkspace(path)
	if err != nil || preview.Exists || preview.Path != path || preview.Identity == "" {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("the preview created a folder")
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	after, err := PreviewWorkspace(path)
	if err != nil || !after.Exists || after.Identity == preview.Identity {
		t.Fatal("the preview lost the folder creation state")
	}
}

func TestWorkspacePreviewReportsAFilePath(t *testing.T) {
	parent := t.TempDir()
	for _, name := range []string{"VISION.md", "notes.txt"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(parent, name)
			if err := os.WriteFile(path, []byte("A project goal."), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := PreviewWorkspace(path)
			const want = "This path points to a file. Choose the folder that contains your Project."
			if err == nil || err.Error() != want {
				t.Fatalf("file path error: got %v, want %q", err, want)
			}
		})
	}
}

func TestWorkspacePreviewReportsAMissingParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent", "project")
	_, err := PreviewWorkspace(path)
	const want = "The project folder or its parent must be an accessible directory."
	if err == nil || err.Error() != want {
		t.Fatalf("absent parent error: got %v, want %q", err, want)
	}
}

func TestZeroProjectsPermitServiceSetup(t *testing.T) {
	spine, _ := visionFixture(t)
	status, _, roots, err := onboardProjectDB(OnboardArgs{Section: "project", Spine: spine})
	if err != nil || status != "ok" || len(roots) != 0 {
		t.Fatalf("zero Project setup: %s %v %v", status, roots, err)
	}
	if finding := doctorProjectsFinding(t, spine); finding.Status != "ok" {
		t.Fatalf("zero Project check: %+v", finding)
	}
}

func TestWorkspaceVisionRejectsAReplacedFolder(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(root)
	identity := workspaceDirectoryIdentity(info, true)
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveWorkspaceVision(root, "absent", "A project goal.", identity); err == nil {
		t.Fatal("the save accepted a replacement folder")
	}
	if _, err := os.Stat(filepath.Join(root, "VISION.md")); !os.IsNotExist(err) {
		t.Fatal("the save changed the replacement folder")
	}
}

func TestWorkspacePreviewAcceptsFinalSlash(t *testing.T) {
	parent, _ := filepath.EvalSymlinks(t.TempDir())
	for _, path := range []string{parent, filepath.Join(parent, "new-project")} {
		want, err := PreviewWorkspace(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := PreviewWorkspace(path + "/")
		if err != nil || got.Path != want.Path || got.Identity != want.Identity {
			t.Fatalf("preview with a final slash: %+v, %v", got, err)
		}
	}
}

func TestWorkspaceVisionRejectsRedirectedRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(root)
	identity := workspaceDirectoryIdentity(info, true)
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root+"-moved", root); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveWorkspaceVision(root, "absent", "A project goal.", identity); err == nil {
		t.Fatal("the save accepted a redirected folder")
	}
}
