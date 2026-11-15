package verbs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProjectVisionListUsesCurrentFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ProjectVisionFile)
	rows := []map[string]any{
		{"id": "project", "status": "paused", "workspace_root": root, "vision": ProjectVisionPresent},
		{"id": "archived", "status": "archived", "workspace_root": root},
	}
	list := map[string]any{"projects": rows}
	check := func(state string, ready bool) {
		t.Helper()
		statuses := InspectProjectListVision(list)
		if len(statuses) != 1 || statuses[0].State != state || statuses[0].Ready != ready || statuses[0].Path != path {
			t.Fatalf("current file status = %+v", statuses)
		}
		if rows[0]["vision_status"] != statuses[0] {
			t.Fatalf("list status differs from the shared result: %+v", rows[0])
		}
		for _, row := range rows[1:] {
			if _, exists := row["vision_status"]; exists {
				t.Fatalf("exempt Project received a status: %+v", row)
			}
		}
	}
	check(ProjectVisionAbsent, false)
	const vision = "Help people understand the local environment."
	if err := os.WriteFile(path, []byte(vision), 0o600); err != nil {
		t.Fatal(err)
	}
	check(ProjectVisionPresent, true)
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), vision) {
		t.Fatal("the file catalog includes the vision content")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check(ProjectVisionAbsent, false)
}

func TestProjectVisionListAcceptsEmptyRegistry(t *testing.T) {
	statuses := InspectProjectListVision(map[string]any{"projects": []map[string]any{}})
	if statuses == nil || len(statuses) != 0 {
		t.Fatalf("empty registry status = %+v", statuses)
	}
}

func TestDoctorVisionSeparatesSetupFromPermissionToWork(t *testing.T) {
	for _, setup := range []string{"ok", "fail"} {
		t.Run(setup, func(t *testing.T) {
			original := DoctorFinding{Check: "spine", Status: "ok", Detail: "The database is available."}
			findings := []DoctorFinding{original, {Check: "projects", Status: setup, Detail: "The workspace setup result."}}
			report := map[string]any{"ok": setup == "ok", "findings": findings}
			statuses := []ProjectVisionStatus{{Project: "project", State: ProjectVisionAbsent, Path: "/workspace/project/VISION.md", Reason: "VISION.md is absent."}}
			ApplyDoctorProjectVision(report, statuses)
			if report["ok"] != false || !reflect.DeepEqual(findings[0], original) {
				t.Fatalf("the composed report lost other results: %+v", report)
			}
			finding := findings[1]
			if finding.Status != "fail" || finding.SetupComplete != (setup == "ok") || len(finding.ProjectVision) != 1 {
				t.Fatalf("the file block lost the setup result: %+v", finding)
			}
			for _, text := range []string{"Work cannot start", "project", statuses[0].Path, statuses[0].Reason, "Your agent can help"} {
				if !strings.Contains(finding.Detail, text) {
					t.Fatalf("the file block omits %q: %s", text, finding.Detail)
				}
			}
		})
	}
}

func TestDoctorValidVisionPreservesOtherFailures(t *testing.T) {
	findings := []DoctorFinding{
		{Check: "spine", Status: "fail", Detail: "The database requires repair."},
		{Check: "projects", Status: "ok", Detail: "The workspace setup is complete."},
	}
	report := map[string]any{"ok": false, "findings": findings}
	ApplyDoctorProjectVision(report, []ProjectVisionStatus{{Project: "project", State: ProjectVisionPresent, Ready: true}})
	if report["ok"] != false || findings[1].Status != "ok" || !findings[1].SetupComplete {
		t.Fatalf("a valid vision changed other requirements: %+v", report)
	}
}

func TestDoctorVisionUnavailablePreservesReport(t *testing.T) {
	findings := []DoctorFinding{
		{Check: "spine", Status: "fail", Detail: "The database requires repair."},
		{Check: "projects", Status: "ok", Detail: "The workspace setup is complete."},
	}
	report := map[string]any{"ok": true, "findings": findings}
	ApplyDoctorVisionUnavailable(report)
	if report["ok"] != false || len(findings) != 2 || findings[0].Detail != "The database requires repair." {
		t.Fatalf("the file check failure lost the report: %+v", report)
	}
	if findings[1].Status != "fail" || !strings.Contains(findings[1].Detail, "cannot check VISION.md") {
		t.Fatalf("the report omits the file check failure: %+v", findings[1])
	}
}
