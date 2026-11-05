package main

import (
	"encoding/json"

	"mc/verbs"
)

func applyProjectVision(req projectSetupRequest, row map[string]any) (any, error) {
	if req.SubmissionID == "" {
		return nil, verbs.Usagef("The Vision submission is required.")
	}
	request := map[string]any{"id": req.ID, "submission_id": req.SubmissionID}
	result, err := setupCommandInput(request, "project", "vision-claim-apply")
	if err != nil {
		return nil, err
	}
	var submission struct {
		ID                string `json:"id"`
		SubmissionID      string `json:"submission_id"`
		Content           string `json:"content"`
		WorkspaceIdentity string `json:"workspace_identity"`
		WorkspaceRevision string `json:"workspace_revision"`
		Status            string `json:"status"`
	}
	body, err := json.Marshal(result["submission"])
	if err != nil || json.Unmarshal(body, &submission) != nil || submission.ID != req.ID || submission.SubmissionID != req.SubmissionID {
		return nil, verbs.Domainf("The Vision submission is invalid.")
	}
	if submission.Status == "saved" || submission.Status == "failed" {
		return setupCommandInput(map[string]any{"id": req.ID}, "project", "vision-draft")
	}
	if submission.Status != "pending" {
		return nil, verbs.Domainf("The Vision submission is unavailable.")
	}
	path, _ := row["workspace_root"].(string)
	var saved verbs.WorkspaceVision
	if row["status"] == "archived" {
		err = verbs.Domainf("An archived Project permits inspection only.")
	} else {
		var preview verbs.WorkspacePreview
		preview, err = verbs.PreviewWorkspace(path)
		if err == nil && (preview.Path != path || preview.Identity != submission.WorkspaceIdentity) {
			err = verbs.Domainf("The Project folder changed. Compare the workspace before another save.")
		}
		if err == nil && result["already_attempted"] == true {
			saved = preview.Vision
			if saved.Content != submission.Content || saved.Revision == "absent" {
				err = verbs.Domainf("The Vision save was interrupted. Compare the workspace before you retry.")
			}
		} else if err == nil {
			saved, err = verbs.SaveWorkspaceVision(path, submission.WorkspaceRevision, submission.Content, submission.WorkspaceIdentity)
		}
		if err == nil && (saved.Content != submission.Content || !saved.Check.Ready) {
			err = verbs.Domainf("The workspace does not contain the submitted Vision. Compare the workspace before another save.")
		}
	}
	if err != nil {
		request["error"] = err.Error()
		if _, recordErr := setupCommandInput(request, "project", "vision-fail"); recordErr != nil {
			return nil, recordErr
		}
	} else {
		request["workspace_revision"] = saved.Revision
		if _, err := setupCommandInput(request, "project", "vision-complete"); err != nil {
			return nil, err
		}
	}
	return setupCommandInput(map[string]any{"id": req.ID}, "project", "vision-draft")
}
