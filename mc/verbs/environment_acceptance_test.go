package verbs

import (
	"strings"
	"testing"
)

func TestEnvironmentLimitsPermitImport(t *testing.T) {
	for _, details := range []string{
		"The repository has no executable build or test command.",
		"The container denies user namespaces. Every repository test remains unverified.",
		"The build returned exit 1. The build remains failed.",
		"The required dependency cache is absent. The repository requires its prepared cache.",
		"The application requires macOS. Its tests remain unverified.",
	} {
		t.Run(details, func(t *testing.T) {
			db := environmentFixture(t)
			op := environmentWork(t, db)[0]
			report := EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "Preparation is complete with environment limits.", Details: details, FilesDigest: environmentTestFiles}
			environmentCall(t, db, environmentSetupID(op), "report", report)
			environmentCall(t, db, environmentSetupID(op), "report", report)
			accept := environmentWork(t, db)[0]
			if accept.Kind != "accept" {
				t.Fatalf("The report did not queue source acceptance: %+v", accept)
			}
			environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: accept.ID, InputsDigest: accept.InputsDigest, Status: "succeeded", FilesDigest: environmentTestFiles, SourceCommit: environmentTestCommit})
			view := environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: op.ProjectID})
			if view["environment"].(EnvironmentState).SetupStatus != "ready" {
				t.Fatal("The limit prevented import.")
			}
			reports := view["results"].([]map[string]any)
			if len(reports) != 1 || reports[0]["details"] != details || reports[0]["operation_id"] != op.ID || reports[0]["image_digest"] != op.ImageDigest {
				t.Fatalf("The stored report lost its evidence: %+v", reports)
			}
		})
	}
}

func TestEnvironmentInitialSetupScriptNeedsNoApproval(t *testing.T) {
	db := environmentFixture(t)
	op := environmentWork(t, db)[0]
	configuration := op.Configuration
	configuration.Setup = EnvironmentCommand{Directory: ".", Command: "npm ci"}
	proposal := EnvironmentRequest{OperationID: op.ID, Outcome: "proposal", Configuration: &configuration, Summary: "The setup script installs the Project dependencies."}
	if _, err := ProjectEnvironment(db, environmentSetupID(op), "report", proposal); err == nil || !strings.Contains(err.Error(), "An initial proposal must change the image inputs.") {
		t.Fatalf("An initial proposal without an image change requested approval: %v", err)
	}
	report := EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Configuration: &configuration, Summary: "The setup script installs the Project dependencies.", FilesDigest: environmentTestFiles}
	view := environmentCall(t, db, environmentSetupID(op), "report", report)
	state := view["environment"].(EnvironmentState)
	if state.Phase == "approval" {
		t.Fatal("A setup script change requested operator approval.")
	}
	accept := environmentWork(t, db)[0]
	if accept.Kind != "accept" || accept.Configuration.Setup.Command != "npm ci" {
		t.Fatalf("The complete report did not queue acceptance of the setup script: %+v", accept)
	}
}

func TestEnvironmentDecisionRestrictsInitialContinuationAndRetries(t *testing.T) {
	for _, decision := range []string{"approve", "deny"} {
		t.Run(decision, func(t *testing.T) {
			db := environmentFixture(t)
			first := environmentWork(t, db)[0]
			proposal := first.Configuration
			proposal.Dockerfile = &EnvironmentFile{Path: ".culler/Dockerfile", Content: "FROM " + environmentBaseReference(environmentTestBase) + "\nRUN true\n"}
			proposal.BuildFiles = []EnvironmentFile{{Path: ".culler/tool.txt", Content: "proposed tools"}}
			environmentCall(t, db, environmentSetupID(first), "report", EnvironmentRequest{OperationID: first.ID, Outcome: "proposal", Configuration: &proposal, Summary: "The image adds the required compiler.", Details: "The current compiler is unavailable."})
			view := environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: first.ProjectID})
			revision := view["draft"].(*EnvironmentRevision).ID
			request := EnvironmentRequest{ProjectID: first.ProjectID, RevisionID: revision}
			stale := request
			stale.RevisionID = first.RevisionID
			if _, err := ProjectEnvironment(db, nil, decision, stale); err == nil {
				t.Fatal("A stale image decision succeeded.")
			}
			if _, err := ProjectEnvironment(db, environmentSetupID(first), decision, request); err == nil {
				t.Fatal("An agent decided its own proposal.")
			}
			environmentCall(t, db, nil, decision, request)
			environmentCall(t, db, nil, decision, request)
			op := environmentWork(t, db)[0]
			expectedImage := first.ImageDigest
			if decision == "approve" {
				if op.Kind != "build" {
					t.Fatal("Approval did not queue a build.")
				}
				if _, err := ProjectEnvironment(db, nil, "operation-result", EnvironmentRequest{OperationID: op.ID, InputsDigest: op.InputsDigest, Status: "succeeded", ImageDigest: environmentTestImage}); err == nil {
					t.Fatal("An incompatible image started an agent.")
				}
				environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: op.ID, InputsDigest: op.InputsDigest, Status: "succeeded", ImageDigest: environmentTestImage, RuntimeCompatible: true})
				op = environmentWork(t, db)[0]
				expectedImage = environmentTestImage
			}
			for attempt := 1; attempt <= 2; attempt++ {
				if op.Kind != "validate" || op.ImageDigest != expectedImage || !strings.Contains(op.Brief, "required outcome is `complete`") || !strings.Contains(op.Brief, "current compiler is unavailable") {
					t.Fatalf("The continuation lost its assignment: %+v", op)
				}
				if decision == "deny" && (op.Configuration.Dockerfile != nil || len(op.Configuration.BuildFiles) != 0) {
					t.Fatal("The denial retained proposed image files in its configuration.")
				}
				if _, err := ProjectEnvironment(db, environmentSetupID(op), "report", EnvironmentRequest{OperationID: op.ID, Outcome: "proposal", Configuration: &proposal, Summary: "The image needs another compiler."}); err == nil {
					t.Fatal("The initial continuation proposed another image.")
				}
				environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: op.ID, InputsDigest: op.InputsDigest, Status: "failed", Error: "The agent connection failed."})
				dvExec(t, db, `UPDATE environment_operations SET retry_at=NULL`)
				if attempt < 2 {
					op = environmentWork(t, db)[0]
				}
			}
			environmentCall(t, db, nil, "retry", EnvironmentRequest{ProjectID: first.ProjectID})
			op = environmentWork(t, db)[0]
			if op.ImageDigest != expectedImage || !strings.Contains(op.Brief, "required outcome is `complete`") {
				t.Fatal("The manual retry lost the image decision.")
			}
			view = environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: first.ProjectID})
			decisions := view["decisions"].([]map[string]any)
			if len(decisions) != 1 || decisions[0]["revision_id"] != revision || decisions[0]["current_image_digest"] != first.ImageDigest || decisions[0]["created_at"] == "" {
				t.Fatalf("The decision lost its identity: %+v", decisions)
			}
			environmentCall(t, db, environmentSetupID(op), "report", EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "Preparation is complete with unavailable platform tools.", Details: "The compiler works. The platform test remains unverified.", FilesDigest: environmentTestFiles})
			accept := environmentWork(t, db)[0]
			if decision == "deny" && accept.Metadata["proposal_operation_id"] != first.ID {
				t.Fatal("Source acceptance lost the denied proposal identity.")
			}
			environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: accept.ID, InputsDigest: accept.InputsDigest, Status: "succeeded", FilesDigest: environmentTestFiles, SourceCommit: environmentTestCommit})
			view = environmentCall(t, db, nil, "get", EnvironmentRequest{ProjectID: first.ProjectID})
			if view["environment"].(EnvironmentState).SelectedImageDigest != expectedImage {
				t.Fatal("Import selected another image.")
			}
			environmentCall(t, db, nil, "assist", EnvironmentRequest{ProjectID: first.ProjectID, Instruction: "Add the platform tools."})
			later := environmentWork(t, db)[0]
			proposal.SourceCommit = environmentTestCommit
			environmentCall(t, db, environmentSetupID(later), "report", EnvironmentRequest{OperationID: later.ID, Outcome: "proposal", Configuration: &proposal, Summary: "The image adds another Project tool."})
		})
	}
}

func TestEnvironmentReportValidationPreservesIncompleteOperation(t *testing.T) {
	for _, mutate := range []func(*EnvironmentRequest){
		func(r *EnvironmentRequest) { r.Outcome = "blocked" },
		func(r *EnvironmentRequest) { r.Outcome = "other" },
		func(r *EnvironmentRequest) { r.Summary = "" },
		func(r *EnvironmentRequest) { r.Summary = "First. Second. Third." },
		func(r *EnvironmentRequest) { r.Summary = strings.Repeat("word ", 26) },
		func(r *EnvironmentRequest) { r.FilesDigest = "" },
	} {
		db := environmentFixture(t)
		op := environmentWork(t, db)[0]
		report := EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "Preparation is complete.", FilesDigest: environmentTestFiles}
		mutate(&report)
		if _, err := ProjectEnvironment(db, environmentSetupID(op), "report", report); err == nil {
			t.Fatalf("An invalid report succeeded: %+v", report)
		}
		if dfStr(t, db, `SELECT status FROM environment_operations WHERE id=?`, op.ID) != "queued" {
			t.Fatal("A refused report completed its operation.")
		}
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM environment_results`).Scan(&count); err != nil || count != 0 {
			t.Fatal("A refused report retained a result.")
		}
	}
}

func TestEnvironmentReportRedactsBeforePersistence(t *testing.T) {
	t.Setenv("FIXTURE_SECRET", "private-value")
	db := environmentFixture(t)
	op := environmentWork(t, db)[0]
	report := EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "Preparation is complete.", Details: "private-value\napi_key=private-key\nBearer private-token\nhttps://user:password@example.test/path", FilesDigest: environmentTestFiles}
	environmentCall(t, db, environmentSetupID(op), "report", report)
	environmentCall(t, db, environmentSetupID(op), "report", report)
	details := dfStr(t, db, `SELECT details FROM environment_results`)
	for _, secret := range []string{"private-value", "private-key", "private-token", "user:password"} {
		if strings.Contains(details, secret) {
			t.Fatalf("The report retained %s.", secret)
		}
	}
	if !strings.Contains(details, "[redacted]") {
		t.Fatal("The report lost the redaction marker.")
	}
}

func TestEnvironmentAcceptancePreservesExplicitPause(t *testing.T) {
	for _, all := range []bool{false, true} {
		db := environmentFixture(t)
		op := environmentWork(t, db)[0]
		if all {
			if _, err := SetEveryProjectPause(db, nil, true); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, err := ProjectSetStatus(db, nil, op.ProjectID, "paused"); err != nil {
				t.Fatal(err)
			}
		}
		environmentCall(t, db, environmentSetupID(op), "report", EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Summary: "Preparation is complete.", FilesDigest: environmentTestFiles})
		accept := environmentWork(t, db)[0]
		environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: accept.ID, InputsDigest: accept.InputsDigest, Status: "succeeded", FilesDigest: environmentTestFiles, SourceCommit: environmentTestCommit})
		if dfStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`) != "paused" || dfStr(t, db, `SELECT setup_status FROM projects WHERE id='ws-test'`) != "ready" {
			t.Fatal("Source acceptance lost the explicit pause or left import incomplete.")
		}
	}
}

func TestEnvironmentEditedProposalRetainsInitialDecision(t *testing.T) {
	db := environmentFixture(t)
	first := environmentWork(t, db)[0]
	proposal := first.Configuration
	proposal.Dockerfile = &EnvironmentFile{Path: ".culler/Dockerfile", Content: "FROM " + environmentBaseReference(environmentTestBase) + "\nRUN true\n"}
	view := environmentCall(t, db, environmentSetupID(first), "report", EnvironmentRequest{OperationID: first.ID, Outcome: "proposal", Configuration: &proposal, Summary: "The image adds the required compiler."})
	revision := view["draft"].(*EnvironmentRevision).ID
	proposal.Dockerfile.Content += "RUN echo tool\n"
	view = environmentCall(t, db, nil, "save", EnvironmentRequest{ProjectID: first.ProjectID, ExpectedRevisionID: revision, Configuration: &proposal})
	edited := view["draft"].(*EnvironmentRevision).ID
	request := EnvironmentRequest{ProjectID: first.ProjectID, RevisionID: edited}
	view = environmentCall(t, db, nil, "approve", request)
	decisions := view["decisions"].([]map[string]any)
	if len(decisions) != 1 || decisions[0]["revision_id"] != edited || decisions[0]["operation_id"] != first.ID {
		t.Fatalf("The edited approval lost its original operation: %+v", decisions)
	}
	build := environmentWork(t, db)[0]
	environmentCall(t, db, nil, "operation-result", EnvironmentRequest{OperationID: build.ID, InputsDigest: build.InputsDigest, Status: "succeeded", ImageDigest: environmentTestImage, RuntimeCompatible: true})
	op := environmentWork(t, db)[0]
	if !strings.Contains(op.Brief, "required outcome is `complete`") {
		t.Fatal("The edited approval lost its continuation restriction.")
	}
	prepared := op.Configuration
	prepared.Setup.Command = "install-available-dependencies"
	environmentCall(t, db, environmentSetupID(op), "report", EnvironmentRequest{OperationID: op.ID, Outcome: "complete", Configuration: &prepared, Summary: "Preparation is complete with platform limits.", FilesDigest: environmentTestFiles})
	environmentCall(t, db, nil, "approve", request)
	if got := environmentWork(t, db); len(got) != 1 || got[0].Kind != "accept" {
		t.Fatal("A repeated approval changed source acceptance.")
	}
}

func TestEnvironmentDenialOfEditedProposalRetainsBothFileSets(t *testing.T) {
	db := environmentFixture(t)
	first := environmentWork(t, db)[0]
	proposal := first.Configuration
	proposal.Dockerfile = &EnvironmentFile{Path: ".culler/Dockerfile", Content: "FROM " + environmentBaseReference(environmentTestBase) + "\nRUN true\n"}
	proposal.BuildFiles = []EnvironmentFile{{Path: "original-tool", Content: "original proposal"}}
	view := environmentCall(t, db, environmentSetupID(first), "report", EnvironmentRequest{OperationID: first.ID, Outcome: "proposal", Configuration: &proposal, Summary: "The image adds the required compiler."})
	revision := view["draft"].(*EnvironmentRevision).ID
	proposal.BuildFiles = []EnvironmentFile{{Path: "edited-tool", Content: "edited proposal"}}
	view = environmentCall(t, db, nil, "save", EnvironmentRequest{ProjectID: first.ProjectID, ExpectedRevisionID: revision, Configuration: &proposal})
	edited := view["draft"].(*EnvironmentRevision).ID
	request := EnvironmentRequest{ProjectID: first.ProjectID, RevisionID: edited}
	environmentCall(t, db, nil, "deny", request)
	environmentCall(t, db, nil, "deny", request)
	op := environmentWork(t, db)[0]
	if op.ImageDigest != first.ImageDigest || !strings.Contains(op.Brief, edited) || op.Metadata["proposal_operation_id"] != first.ID {
		t.Fatal("The edited denial lost its exact decision or current image.")
	}
	files := op.Metadata["denied_build_files"].([]any)
	if len(files) != 3 {
		t.Fatalf("The edited denial lost proposed files: %+v", files)
	}
}
