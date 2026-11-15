package verbs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mc/substrate"
)

// writeVisionFixture gives a test workspace the North Star file, so a fixture
// that is about something else registers as a fully configured Project
// instead of tripping the VISION.md question.
func writeVisionFixture(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ProjectVisionFile), []byte("# Vision\n\nShip it.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// visionFixture is the first-Project fixture every arm below starts from:
// a provisioned MC_HOME and spine, and a workspace repository with NO
// VISION.md, so each arm chooses what the operator does about it.
func visionFixture(t *testing.T) (spine, root string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "mc-home")
	spine = filepath.Join(home, "spine.db")
	root = filepath.Join(t.TempDir(), "workspace")
	t.Setenv("MC_HOME", home)
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := gitAtSource(root, "init", "--quiet"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := onboardHome(spine, true); err != nil {
		t.Fatal(err)
	}
	return spine, root
}

func storedVision(t *testing.T, spine, project string) string {
	t.Helper()
	db, err := substrate.Open(spine)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var vision string
	if err := db.QueryRow(`SELECT vision FROM projects WHERE id = ?`, project).Scan(&vision); err != nil {
		t.Fatal(err)
	}
	return vision
}

func doctorProjectsFinding(t *testing.T, spine string) DoctorFinding {
	t.Helper()
	res, err := Doctor(nil, spine)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range res.(map[string]any)["findings"].([]doctorFinding) {
		if finding.Check == "projects" {
			return finding
		}
	}
	t.Fatal("doctor emitted no projects finding")
	return DoctorFinding{}
}

func TestProjectVisionChecksCurrentFile(t *testing.T) {
	root := t.TempDir()
	path := ProjectVisionPath(root)
	cases := []struct{ name, body, state string }{
		{"empty", " \n", ProjectVisionEmpty},
		{"template", projectVisionTemplate, ProjectVisionTemplate},
		{"comments", "# Vision\n<!-- Fill this later. -->", ProjectVisionTemplate},
		{"goal", "Help small teams make useful software.", ProjectVisionPresent},
		{"goal heading", "# Help small teams make useful software", ProjectVisionPresent},
		{"generated purpose", "# VISION.md\n\n" + generatedVisionPurpose + "\n\n## Purpose\n<!-- Add text -->", ProjectVisionTemplate},
	}
	if got := CheckProjectVision(root); got.State != ProjectVisionAbsent || got.Ready || got.Path != path {
		t.Fatalf("Absent file: %+v", got)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			got := CheckProjectVision(root)
			if got.State != tc.state || got.Ready != (tc.state == ProjectVisionPresent) {
				t.Fatalf("Check: %+v", got)
			}
			if got.Ready && got.Content != tc.body {
				t.Fatalf("The snapshot differs: %+v", got)
			}
		})
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	if got := CheckProjectVision(root); got.State != ProjectVisionUnreadable || got.Ready {
		t.Fatalf("Unreadable file: %+v", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := CheckProjectVision(root); got.State != ProjectVisionAbsent || got.Ready {
		t.Fatalf("Removed file: %+v", got)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := CheckProjectVision(root); got.State != ProjectVisionUnreadable {
		t.Fatalf("Directory: %+v", got)
	}
}

func TestOnboardProjectOffersVisionChoices(t *testing.T) {
	spine, root := visionFixture(t)
	args := OnboardArgs{Spine: spine, Project: "primary", WorkspaceRoot: root, AcceptGeneratedSandbox: true}
	_, err := prepareOnboardProject(args)
	if err == nil || !strings.Contains(err.Error(), ProjectVisionExplanation) || !strings.Contains(err.Error(), "create|later") {
		t.Fatalf("Missing choice: %v", err)
	}
	args.ProjectVisionAnswer = ProjectVisionAnswerLater
	status, detail, err := onboardProject(args)
	if err != nil || status != "done" || !strings.Contains(detail, "Work cannot start") {
		t.Fatalf("Deferred vision: %s %s %v", status, detail, err)
	}
	if got := storedVision(t, spine, "primary"); got != ProjectVisionDeclined {
		t.Fatalf("Stored state: %s", got)
	}
	if _, err := os.Stat(ProjectVisionPath(root)); !os.IsNotExist(err) {
		t.Fatalf("Deferral created a file: %v", err)
	}
}

func TestOnboardProjectTemplateRemainsBlocked(t *testing.T) {
	spine, root := visionFixture(t)
	args := OnboardArgs{Spine: spine, Project: "primary", WorkspaceRoot: root, AcceptGeneratedSandbox: true, ProjectVisionAnswer: ProjectVisionAnswerCreate}
	status, detail, err := onboardProject(args)
	if err != nil || status != "done" || !strings.Contains(detail, "Work cannot start") {
		t.Fatalf("Template: %s %s %v", status, detail, err)
	}
	if got := CheckProjectVision(root); got.State != ProjectVisionTemplate || got.Ready {
		t.Fatalf("Template check: %+v", got)
	}
	if got := storedVision(t, spine, "primary"); got != ProjectVisionUnknown {
		t.Fatalf("Template state: %s", got)
	}
	writeVisionFixture(t, root)
	before, err := os.ReadFile(ProjectVisionPath(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, answer := range []string{ProjectVisionAnswerCreate, ProjectVisionAnswerLater, ""} {
		args.ProjectVisionAnswer = answer
		if _, _, err := onboardProject(args); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(ProjectVisionPath(root))
		if err != nil || string(after) != string(before) {
			t.Fatalf("Setup changed the file: %v", err)
		}
	}
	if got := storedVision(t, spine, "primary"); got != ProjectVisionPresent {
		t.Fatalf("Valid vision state: %s", got)
	}
}

func TestProjectVisionRequiresAbsoluteRootAndBoundedContent(t *testing.T) {
	for _, root := range []string{"", ".", "workspace"} {
		if got := CheckProjectVision(root); got.State != ProjectVisionUnknown || got.Ready {
			t.Fatalf("Relative root accepted: %+v", got)
		}
	}
	root := t.TempDir()
	if err := os.WriteFile(ProjectVisionPath(root), []byte(strings.Repeat("<", maxProjectVisionBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	got := CheckProjectVision(root)
	if got.Ready || got.State != ProjectVisionUnreadable || !strings.Contains(got.Reason, "64 KiB") || got.Content != "" {
		t.Fatalf("Large file accepted: %+v", got)
	}
}
