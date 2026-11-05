package verbs

import "testing"

func TestEnvironmentJournalRetainsReportOutcomesAndApproval(t *testing.T) {
	db := environmentFixture(t)
	op := environmentWork(t, db)[0]
	c := environmentTestConfiguration()
	c.Dockerfile = &EnvironmentFile{Path: ".culler/Dockerfile", Content: "FROM " + environmentBaseReference(environmentTestBase) + "\nRUN true\n"}
	view := environmentCall(t, db, environmentSetupID(op), "report", EnvironmentRequest{OperationID: op.ID, Outcome: "proposal", Configuration: &c, Summary: "The image supplies the Project tools."})
	revision := view["draft"].(*EnvironmentRevision).ID
	ops := view["operations"].([]EnvironmentOperation)
	if ops[0].Metadata["report_outcome"] != "proposal" || ops[0].Metadata["proposed_revision_id"] != revision {
		t.Fatalf("The proposal lost its outcome or revision: %+v", ops)
	}
	view = environmentCall(t, db, nil, "approve", EnvironmentRequest{ProjectID: op.ProjectID, RevisionID: revision})
	approvals := view["approvals"].([]map[string]any)
	if len(approvals) != 1 || approvals[0]["revision_id"] != revision || approvals[0]["created_at"] == "" {
		t.Fatalf("The approval evidence is absent: %+v", approvals)
	}
	build := environmentWork(t, db)[0]
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: build.ID, InputsDigest: build.InputsDigest, Status: "succeeded", ImageDigest: environmentTestImage, RuntimeCompatible: true})
	continued := environmentWork(t, db)[0]
	view = environmentCall(t, db, environmentSetupID(continued), "report", EnvironmentRequest{OperationID: continued.ID, Outcome: "complete", Summary: "The required service is unavailable.", FilesDigest: environmentTestFiles})
	ops = view["operations"].([]EnvironmentOperation)
	if ops[1].Status != "succeeded" || ops[1].Metadata["report_outcome"] != "complete" || ops[1].Error != "" {
		t.Fatalf("The complete report has the wrong outcome: %+v", ops[0])
	}
	if ops[len(ops)-1].Metadata["proposed_revision_id"] != revision {
		t.Fatal("The later operation replaced the proposal evidence.")
	}
}

func TestEnvironmentJournalRetainsReportAndInitialAcceptance(t *testing.T) {
	db := environmentFixture(t)
	op := environmentWork(t, db)[0]
	c := environmentTestConfiguration()
	details := "The repository test returned exit 1. The test remains failed."
	environmentCall(t, db, environmentSetupID(op), "report", EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "Preparation is complete with a test failure.", Details: details, Configuration: &c, FilesDigest: environmentTestFiles})
	accept := environmentWork(t, db)[0]
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: accept.ID, InputsDigest: accept.InputsDigest, Status: "succeeded", SourceCommit: environmentTestCommit, FilesDigest: environmentTestFiles})
	view := environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: op.ProjectID})
	if view["environment"].(EnvironmentState).SetupStatus != "ready" {
		t.Fatal("The read response omits completed initialization.")
	}
	result := view["results"].([]map[string]any)[0]
	if result["details"] != details {
		t.Fatalf("The report details are absent: %+v", result)
	}
	accepted := view["operations"].([]EnvironmentOperation)[0]
	if accepted.Metadata["initial_setup"] != true || accepted.Metadata["source_commit"] != environmentTestCommit || accepted.EndedAt == "" {
		t.Fatalf("The initial acceptance evidence is incomplete: %+v", accepted)
	}
	view = environmentCall(t, db, nil, "save", EnvironmentRequest{ProjectID: op.ProjectID, ExpectedRevisionID: accepted.RevisionID, Configuration: &c, Explanation: "A later environment draft."})
	retained := view["operations"].([]EnvironmentOperation)[0]
	if retained.ID != accepted.ID || retained.RevisionID != accepted.RevisionID || retained.EndedAt != accepted.EndedAt {
		t.Fatal("The later draft changed initial acceptance evidence.")
	}
	if len(view["vision"].(map[string]any)) != 0 {
		t.Fatal("The journal invented absent Vision evidence.")
	}
}
