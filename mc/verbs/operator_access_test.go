package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"mc/dispatch"
)

func operatorSession(t *testing.T, db *sql.DB, name string, permissions []string) *RunIdentity {
	t.Helper()
	body, err := json.Marshal(permissions)
	if err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `INSERT INTO collie_sessions(id,container_name,verb_allowlist,session_path,binding)
		VALUES(?,?,?,?,?)`, name, "mc-collie-"+name, string(body), "sessions/"+name, dvCollieBinding(t))
	return &RunIdentity{Tier: "collie", RunID: name, VerbAllowlist: permissions}
}

func TestOperatorAuthorityPreservesFrozenSessions(t *testing.T) {
	db := dvSpine(t)
	id := operatorSession(t, db, "restricted", []string{"project.pause"})
	if _, err := ProjectSetStatus(db, id, "ws-test", "paused"); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectSetStatus(db, id, "ws-test", "active"); err == nil {
		t.Fatal("A restricted session resumed the Project.")
	}
	forged := *id
	forged.VerbAllowlist = MaximumCollieAgentVerbs()
	if _, err := ProjectSetStatus(db, &forged, "ws-test", "active"); err == nil {
		t.Fatal("A forged launch replaced the frozen permissions.")
	}
	if err := AuthorizeOperator(context.Background(), db, id, "", ""); err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `UPDATE collie_sessions SET status='ended' WHERE id=?`, id.RunID)
	if _, err := ProjectSetStatus(db, id, "ws-test", "paused"); err == nil {
		t.Fatal("An ended session changed a Project.")
	}
	if err := AuthorizeOperator(context.Background(), db, id, "", ""); err == nil {
		t.Fatal("An ended session passed the current authority check.")
	}
}

func TestOperatorPauseKeepsIncompleteProjectsPaused(t *testing.T) {
	db := dvSpine(t)
	id := operatorSession(t, db, "operator", MaximumCollieAgentVerbs())
	dvAddProject(t, db, "incomplete", "paused")
	dvExec(t, db, `UPDATE projects SET setup_status='vision' WHERE id='incomplete'`)
	if _, err := SetEveryProjectPause(db, id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := SetEveryProjectPause(db, id, false); err != nil {
		t.Fatal(err)
	}
	if got := dfStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`); got != "active" {
		t.Fatalf("The ready Project has status %s.", got)
	}
	if got := dfStr(t, db, `SELECT status FROM projects WHERE id='incomplete'`); got != "paused" {
		t.Fatalf("The incomplete Project has status %s.", got)
	}
}

func TestVisionConversationRefusesGlobalProjectControls(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "another-project", "active")
	id := &RunIdentity{Tier: "collie", RunID: "vision-global-controls", VerbAllowlist: MaximumCollieAgentVerbs()}
	permissions, err := json.Marshal(id.VerbAllowlist)
	if err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `INSERT INTO collie_sessions(id,vision_project_id,container_name,verb_allowlist,session_path,binding)
		VALUES(?,'ws-test','mc-collie-vision-global',?,'sessions/vision-global',?)`, id.RunID, string(permissions), dvCollieBinding(t))
	for _, action := range []string{"pause", "resume"} {
		if _, err := OperatorRequestSubmit(db, id, "vision-global-"+action, []string{action}, "", "dev", "collie"); err == nil {
			t.Fatalf("The Vision session submitted a global %s.", action)
		}
		if _, err := SetEveryProjectPause(db, id, action == "pause"); err == nil {
			t.Fatalf("The direct Vision call admitted a global %s.", action)
		}
	}
	for _, project := range []string{"ws-test", "another-project"} {
		if status := dfStr(t, db, `SELECT status FROM projects WHERE id=?`, project); status != "active" {
			t.Fatalf("A refused control changed %s to %s.", project, status)
		}
	}
	if count := dfInt(t, db, `SELECT COUNT(*) FROM operator_requests`); count != 0 {
		t.Fatal("A refused Vision control created a request.")
	}
	if _, err := OperatorRequestSubmit(db, id, "vision-own-pause", []string{"project", "pause", "ws-test"}, "", "dev", "collie"); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"paused", "active"} {
		if _, err := ProjectSetStatus(db, id, "ws-test", status); err != nil {
			t.Fatalf("The Vision session lost its own Project control: %v", err)
		}
	}
	ordinary := operatorSession(t, db, "ordinary-global-controls", MaximumCollieAgentVerbs())
	for _, paused := range []bool{true, false} {
		if _, err := SetEveryProjectPause(db, ordinary, paused); err != nil {
			t.Fatalf("The ordinary session lost its global control: %v", err)
		}
	}
}

func TestOrdinaryCollieEditsAndSubmitsVision(t *testing.T) {
	db := dvSpine(t)
	draft := newVisionDraft(t, db)
	id := operatorSession(t, db, "ordinary", MaximumCollieAgentVerbs())
	result, err := ProjectVision(db, id, "vision-edit", VisionDraftRequest{ID: draft.ID, Revision: draft.Revision, Content: "Build a recipe collection for home cooks."})
	if err != nil {
		t.Fatal(err)
	}
	edited := result.(map[string]any)["draft"].(VisionDraft)
	if _, err := ProjectVision(db, id, "vision-submit", draftSubmission(edited)); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectVision(db, id, "vision-complete", VisionDraftRequest{ID: draft.ID}); err == nil {
		t.Fatal("Collie completed a host-owned Vision save.")
	}
	if _, err := ProjectVision(db, id, "vision-edit", VisionDraftRequest{ID: draft.ID, Revision: draft.Revision, Content: "An obsolete edit."}); err == nil {
		t.Fatal("An obsolete revision replaced the Vision.")
	}
}

func TestPacketConversationCannotDecideAnotherPacket(t *testing.T) {
	db := dvSpine(t)
	packet, version := hpPacket(t, db, 1)
	other, otherVersion := hpPacket(t, db, 2)
	started := hpMustPrepare(t, db, packet, version, false)
	id := &RunIdentity{Tier: "collie", RunID: started.SessionID, VerbAllowlist: MaximumCollieAgentVerbs()}
	if _, err := PacketDecide(db, id, 2, "cancel", "The operator cancelled this task.", other, otherVersion); err == nil || !strings.Contains(err.Error(), "another review packet") {
		t.Fatalf("The other packet decision returned %v.", err)
	}
	if _, err := PacketDecide(db, id, 1, "cancel", "The operator cancelled this task.", packet, version); err != nil {
		t.Fatal(err)
	}
}

func TestOperatorPermissionsExcludeComponentCommands(t *testing.T) {
	db := dvSpine(t)
	id := operatorSession(t, db, "operator", MaximumCollieAgentVerbs())
	for _, permission := range []string{"dispatch", "environment.work", "agent-profile.credential-read", "collie.reply", "project.vision-complete"} {
		if err := AuthorizeOperator(context.Background(), db, id, permission, ""); err == nil {
			t.Fatalf("Collie received component permission %s.", permission)
		}
	}
	if _, err := ProjectEnvironment(db, id, "work", EnvironmentRequest{}); err == nil {
		t.Fatal("Collie started environment execution.")
	}
}

func TestCollieApprovesTheExactEnvironmentProposal(t *testing.T) {
	db := environmentFixture(t)
	id := operatorSession(t, db, "environment-operator", MaximumCollieAgentVerbs())
	op := environmentWork(t, db)[0]
	configuration := environmentTestConfiguration()
	configuration.Dockerfile = &EnvironmentFile{Path: ".culler/Dockerfile", Content: "FROM " + environmentBaseReference(environmentTestBase) + "\nRUN echo prepare\n"}
	view := environmentCall(t, db, environmentSetupID(op), "report", EnvironmentRequest{
		OperationID: op.ID, Outcome: "proposal", Configuration: &configuration, Summary: "The Project requires an additional image command.",
	})
	state := view["environment"].(EnvironmentState)
	if _, err := ProjectEnvironment(db, id, "approve", EnvironmentRequest{ProjectID: "ws-test", RevisionID: "obsolete"}); err == nil {
		t.Fatal("Collie approved an obsolete environment revision.")
	}
	environmentCall(t, db, id, "approve", EnvironmentRequest{ProjectID: "ws-test", RevisionID: state.DraftRevisionID})
	build := environmentWork(t, db)[0]
	if build.Kind != "build" || build.Configuration.Dockerfile.Content != configuration.Dockerfile.Content {
		t.Fatalf("The build differs from the approved proposal: %+v", build)
	}
	if dfStr(t, db, `SELECT status FROM projects WHERE id='ws-test'`) != "paused" {
		t.Fatal("Image approval completed Project initialization.")
	}
}

func TestEndedCollieCannotSaveRoleInstructions(t *testing.T) {
	db := dvSpine(t)
	id := operatorSession(t, db, "role-operator", MaximumCollieAgentVerbs())
	revision := int64(0)
	instructions := "Use the Project check command before submission."
	request := RoleSaveRequest{Project: "ws-test", Role: dispatch.RoleWorker, ExpectedRevision: &revision, Instructions: &instructions}
	if _, err := ProjectRoleSave(db, id, request); err != nil {
		t.Fatal(err)
	}
	revision = 1
	instructions = "An unauthorized replacement."
	dvExec(t, db, `UPDATE collie_sessions SET status='ended' WHERE id=?`, id.RunID)
	if _, err := ProjectRoleSave(db, id, request); err == nil {
		t.Fatal("An ended session replaced the role instructions.")
	}
	if dfInt(t, db, `SELECT MAX(revision) FROM role_customizations WHERE project='ws-test' AND role='worker'`) != 1 {
		t.Fatal("The refused change created another revision.")
	}
}
