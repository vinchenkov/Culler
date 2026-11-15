package verbs

import "strings"

// ProjectVisionStatus identifies the file that blocks a Project.
type ProjectVisionStatus struct {
	Project string `json:"project"`
	State   string `json:"state"`
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	Ready   bool   `json:"ready"`
}

// InspectProjectListVision runs where the workspace files reside.
func InspectProjectListVision(result any) []ProjectVisionStatus {
	statuses := []ProjectVisionStatus{}
	rows := result.(map[string]any)["projects"].([]map[string]any)
	for _, row := range rows {
		if row["status"] == "archived" {
			continue
		}
		root, _ := row["workspace_root"].(string)
		check := CheckProjectVision(root)
		id, _ := row["id"].(string)
		status := ProjectVisionStatus{Project: id, State: check.State, Path: check.Path, Reason: check.Reason, Ready: check.Ready}
		row["vision_status"] = status
		statuses = append(statuses, status)
	}
	return statuses
}

func applyProjectVisionFinding(finding *DoctorFinding, statuses []ProjectVisionStatus) {
	finding.SetupComplete = finding.Status == "ok"
	finding.ProjectVision = statuses
	var blocked []string
	for _, status := range statuses {
		if !status.Ready {
			blocked = append(blocked, status.Project+": "+status.Reason+" Describe your vision in "+status.Path+". Your agent can help you write it.")
		}
	}
	if len(blocked) > 0 {
		finding.Status = "fail"
		finding.Detail += ". Work cannot start: VISION.md is required. " + strings.Join(blocked, " ")
	}
}

// ApplyDoctorProjectVision adds host file facts to the composed report.
func ApplyDoctorProjectVision(result any, statuses []ProjectVisionStatus) {
	report := result.(map[string]any)
	findings := report["findings"].([]DoctorFinding)
	for i := range findings {
		if findings[i].Check == "projects" {
			applyProjectVisionFinding(&findings[i], statuses)
			if findings[i].Status == "fail" {
				report["ok"] = false
			}
		}
	}
}

func markVisionUnavailable(finding *DoctorFinding) {
	finding.Status = "fail"
	finding.Detail += ". Culler cannot check VISION.md. Work requires a successful file check."
}

// ApplyDoctorVisionUnavailable preserves other checks when the file catalog is unavailable.
func ApplyDoctorVisionUnavailable(result any) {
	report := result.(map[string]any)
	for i := range report["findings"].([]DoctorFinding) {
		finding := &report["findings"].([]DoctorFinding)[i]
		if finding.Check == "projects" {
			markVisionUnavailable(finding)
		}
	}
	report["ok"] = false
}
