package verbs

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func draftCall(t *testing.T, db *sql.DB, action string, request VisionDraftRequest) VisionDraft {
	t.Helper()
	result, err := ProjectVision(db, nil, action, request)
	if err != nil {
		t.Fatal(err)
	}
	return result.(map[string]any)["draft"].(VisionDraft)
}

func newVisionDraft(t *testing.T, db *sql.DB) VisionDraft {
	t.Helper()
	dvExec(t, db, `DELETE FROM project_environments WHERE project_id='ws-test'`)
	dvExec(t, db, `UPDATE projects SET status='paused',setup_status='vision' WHERE id='ws-test'`)
	return draftCall(t, db, "vision-init", VisionDraftRequest{ID: "ws-test", Content: "Build a bread recipe collection.", WorkspaceIdentity: "folder", WorkspaceRevision: "absent"})
}

func draftSubmission(d VisionDraft) VisionDraftRequest {
	return VisionDraftRequest{ID: d.ID, Revision: d.Revision, WorkspaceIdentity: d.WorkspaceIdentity, WorkspaceRevision: d.WorkspaceRevision}
}

func TestVisionStartRequiresTheConfirmedDraft(t *testing.T) {
	for _, change := range []string{"unsubmitted", "pending", "failed", "draft", "workspace", "folder", "submission", "archived", "ready"} {
		t.Run(change, func(t *testing.T) {
			db := dvSpine(t)
			draft := newVisionDraft(t, db)
			if change != "unsubmitted" {
				draft = draftCall(t, db, "vision-submit", draftSubmission(draft))
				if change == "failed" {
					draft = draftCall(t, db, "vision-fail", VisionDraftRequest{ID: draft.ID, SubmissionID: draft.Submission.SubmissionID, Error: "The workspace is unavailable."})
				} else if change != "pending" {
					draft = draftCall(t, db, "vision-complete", VisionDraftRequest{ID: draft.ID, SubmissionID: draft.Submission.SubmissionID, WorkspaceRevision: visionRevision([]byte(draft.Content))})
				}
			}
			request := draftSubmission(draft)
			if draft.Submission != nil {
				request.SubmissionID = draft.Submission.SubmissionID
			}
			switch change {
			case "draft":
				draftCall(t, db, "vision-edit", VisionDraftRequest{ID: draft.ID, Revision: draft.Revision, Content: "A different Project goal."})
			case "workspace":
				request.WorkspaceRevision = "external"
			case "folder":
				request.WorkspaceIdentity = "replacement"
			case "submission":
				request.SubmissionID = "another-submission"
			case "archived":
				dvExec(t, db, `UPDATE projects SET status='archived' WHERE id=?`, draft.ID)
			}
			if change != "ready" {
				if _, err := ProjectVision(db, nil, "vision-start", request); err == nil {
					t.Fatal("The incomplete request activated the Project.")
				}
				if dfStr(t, db, `SELECT setup_status FROM projects WHERE id='ws-test'`) == "ready" {
					t.Fatal("The failed start completed initialization.")
				}
				return
			}
			started := draftCall(t, db, "vision-start", request)
			if started.SetupStatus != "configured" || started.ProjectStatus != "paused" || started.SetupID == "" {
				t.Fatalf("Project state: %+v", started)
			}
			repeated := draftCall(t, db, "vision-start", request)
			if repeated.SetupID != started.SetupID || repeated.ProjectStatus != "paused" {
				t.Fatal("A repeated start changed the separate setup or released pipeline work.")
			}
		})
	}
}

func TestVisionDraftCASAndPersistentFile(t *testing.T) {
	db := dvSpine(t)
	first := newVisionDraft(t, db)
	second := draftCall(t, db, "vision-edit", VisionDraftRequest{ID: first.ID, Revision: first.Revision, Content: "Help people bake bread at home."})
	if second.Revision == first.Revision {
		t.Fatal("The edit retained the old revision.")
	}
	if _, err := ProjectVision(db, nil, "vision-edit", VisionDraftRequest{ID: first.ID, Revision: first.Revision, Content: "An obsolete edit"}); err == nil {
		t.Fatal("An obsolete edit replaced the draft.")
	}
	file := filepath.Join(os.Getenv("MC_HOME"), "vision-drafts", first.ID, "draft.json")
	readDisk := func() VisionDraft {
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var stored VisionDraft
		if err := json.Unmarshal(body, &stored); err != nil {
			t.Fatal(err)
		}
		return stored
	}
	if stored := readDisk(); stored.Content != second.Content || stored.Revision != second.Revision {
		t.Fatalf("stored = %+v", stored)
	}
	if err := os.WriteFile(file, []byte("interrupted file"), 0600); err != nil {
		t.Fatal(err)
	}
	reloaded := draftCall(t, db, "vision-draft", VisionDraftRequest{ID: first.ID})
	if reloaded.Content != second.Content || readDisk().Revision != second.Revision {
		t.Fatal("The retained draft did not repair its file.")
	}
	again := draftCall(t, db, "vision-init", VisionDraftRequest{ID: first.ID, Content: "Do not replace", WorkspaceIdentity: "folder", WorkspaceRevision: "absent"})
	if again.Revision != second.Revision {
		t.Fatal("Registration retry replaced the draft.")
	}
	if _, err := ProjectVision(db, nil, "vision-init", VisionDraftRequest{ID: first.ID, WorkspaceIdentity: "another-folder", WorkspaceRevision: "absent"}); err == nil {
		t.Fatal("A different folder reused the draft.")
	}
}

func TestVisionSubmissionFreezesDraftAndKeepsLaterEdits(t *testing.T) {
	db := dvSpine(t)
	draft := newVisionDraft(t, db)
	request := draftSubmission(draft)
	queued := draftCall(t, db, "vision-submit", request)
	if queued.Submission == nil || queued.Submission.Status != "pending" {
		t.Fatalf("queued = %+v", queued)
	}
	if _, err := db.Exec(`UPDATE vision_submissions SET content='Different text' WHERE id=?`, queued.Submission.SubmissionID); err == nil {
		t.Fatal("The database permitted a change to the authorized contents.")
	}
	retry := draftCall(t, db, "vision-submit", request)
	if retry.Submission.SubmissionID != queued.Submission.SubmissionID {
		t.Fatal("A retry created another submission.")
	}
	edited := draftCall(t, db, "vision-edit", VisionDraftRequest{ID: draft.ID, Revision: draft.Revision, Content: "Help people bake whole grain bread."})
	if edited.Submission.Content != draft.Content {
		t.Fatal("An edit changed the authorized contents.")
	}
	if _, err := ProjectVision(db, nil, "vision-submit", draftSubmission(edited)); err == nil {
		t.Fatal("A second save bypassed the pending save.")
	}
	completion := VisionDraftRequest{ID: draft.ID, SubmissionID: queued.Submission.SubmissionID, WorkspaceRevision: visionRevision([]byte(draft.Content))}
	complete := draftCall(t, db, "vision-complete", completion)
	if complete.Content != edited.Content || complete.Revision != edited.Revision || complete.SetupStatus != "configured" || complete.Submission.Status != "saved" {
		t.Fatalf("complete = %+v", complete)
	}
	draftCall(t, db, "vision-complete", completion)
	if dfStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`) != "paused" {
		t.Fatal("The save activated work.")
	}
	newQueue := draftCall(t, db, "vision-submit", draftSubmission(complete))
	oldRetry, err := ProjectVision(db, nil, "vision-submit", request)
	if err != nil {
		t.Fatal(err)
	}
	response := oldRetry.(map[string]any)
	if response["submission"].(VisionSubmission).SubmissionID != queued.Submission.SubmissionID {
		t.Fatal("A retry identified another submission.")
	}
	if response["draft"].(VisionDraft).Submission.SubmissionID != newQueue.Submission.SubmissionID {
		t.Fatal("A retry lost the current draft state.")
	}
}

func TestVisionSubmissionFailureAndComparison(t *testing.T) {
	db := dvSpine(t)
	draft := newVisionDraft(t, db)
	queued := draftCall(t, db, "vision-submit", draftSubmission(draft))
	failed := draftCall(t, db, "vision-fail", VisionDraftRequest{ID: draft.ID, SubmissionID: queued.Submission.SubmissionID, Error: "The workspace changed."})
	if failed.SetupStatus != "vision" || failed.Submission.Status != "failed" {
		t.Fatalf("failed = %+v", failed)
	}
	rebased := draftCall(t, db, "vision-rebase", VisionDraftRequest{ID: draft.ID, Revision: draft.Revision, WorkspaceIdentity: draft.WorkspaceIdentity, WorkspaceRevision: "external-revision"})
	if rebased.Content != draft.Content {
		t.Fatal("Comparison replaced the draft.")
	}
	newQueue := draftCall(t, db, "vision-submit", draftSubmission(rebased))
	if newQueue.Submission.SubmissionID == queued.Submission.SubmissionID {
		t.Fatal("The revised authorization reused an obsolete submission.")
	}
	if _, err := ProjectVision(db, nil, "vision-complete", VisionDraftRequest{ID: draft.ID, SubmissionID: newQueue.Submission.SubmissionID, WorkspaceRevision: "wrong"}); err == nil {
		t.Fatal("An unverified file completed setup.")
	}
}

func TestVisionSetupBlocksEveryResume(t *testing.T) {
	db := dvSpine(t)
	draft := newVisionDraft(t, db)
	for _, stage := range []string{"vision", "configured"} {
		dvExec(t, db, `UPDATE projects SET setup_status=? WHERE id=?`, stage, draft.ID)
		if _, err := ProjectSetStatus(db, nil, draft.ID, "active"); err == nil {
			t.Fatalf("Resume bypassed %s", stage)
		}
		if _, err := SetEveryProjectPause(db, nil, false); err != nil {
			t.Fatal(err)
		}
		if dfStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`) != "paused" {
			t.Fatal("Resume all bypassed setup.")
		}
		if _, err := db.Exec(`UPDATE projects SET status='active' WHERE id=?`, draft.ID); err == nil {
			t.Fatal("The database admitted an invalid active Project.")
		}
	}
}

func TestVisionDraftScopesAndValidation(t *testing.T) {
	db := dvSpine(t)
	draft := newVisionDraft(t, db)
	dvAddProject(t, db, "other", "paused")
	started, err := CollieStart(db, nil, CollieStartArgs{From: "dashboard:vision-ws-test", VisionProject: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	id := &RunIdentity{Tier: "collie", RunID: started.(CollieStartResult).SessionID, VerbAllowlist: MaximumCollieAgentVerbs()}
	if _, err := ProjectVision(db, id, "vision-edit", VisionDraftRequest{ID: draft.ID, Revision: draft.Revision, Content: "Build a recipe archive."}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		action string
		req    VisionDraftRequest
	}{
		{"vision-draft", VisionDraftRequest{ID: "other"}},
		{"vision-init", VisionDraftRequest{ID: draft.ID}},
		{"vision-submit", draftSubmission(draft)},
		{"vision-start", draftSubmission(draft)},
		{"vision-pending", VisionDraftRequest{}},
	} {
		if _, err := ProjectVision(db, id, item.action, item.req); err == nil {
			t.Fatalf("Collie bypassed scope: %s", item.action)
		}
	}
	if _, err := ProjectVision(db, &RunIdentity{Tier: "pipeline"}, "vision-draft", VisionDraftRequest{ID: draft.ID}); err == nil {
		t.Fatal("A pipeline read a private draft.")
	}
	for _, content := range []string{"", "# Vision\n<!-- Add a goal -->", strings.Repeat("a", MaxVisionDraftBytes+1)} {
		current := draftCall(t, db, "vision-draft", VisionDraftRequest{ID: draft.ID})
		result, err := ProjectVision(db, nil, "vision-edit", VisionDraftRequest{ID: draft.ID, Revision: current.Revision, Content: content})
		if len(content) > MaxVisionDraftBytes {
			if err == nil {
				t.Fatal("An oversized draft was accepted.")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ProjectVision(db, nil, "vision-submit", draftSubmission(result.(map[string]any)["draft"].(VisionDraft))); err == nil {
			t.Fatal("A draft without a goal was submitted.")
		}
	}
}
