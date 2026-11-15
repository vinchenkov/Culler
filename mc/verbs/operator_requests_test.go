package verbs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mc/dispatch"
)

func TestOperatorRequestsEnforceAdmissionAndReplay(t *testing.T) {
	db := dvSpine(t)
	id := operatorSession(t, db, "requests", []string{"project.pause"})
	args := []string{"project", "pause", "ws-test"}
	first, err := OperatorRequestSubmit(db, id, "request-1", args, "", "dev", "external")
	if err != nil {
		t.Fatal(err)
	}
	if first.Origin != "collie" || first.SessionID != id.RunID || first.State != "pending" {
		t.Fatalf("Unexpected receipt: %+v", first)
	}
	again, err := OperatorRequestSubmit(db, id, "request-1", args, "", "dev", "external")
	if err != nil || again.ID != first.ID {
		t.Fatalf("The retry changed its request: %+v, %v", again, err)
	}
	if _, err := OperatorRequestSubmit(db, id, "request-1", []string{"project", "resume", "ws-test"}, "", "dev", "external"); err == nil {
		t.Fatal("A request identifier accepted different content.")
	}
	if _, err := OperatorRequestSubmit(db, id, "request-2", []string{"project", "resume", "ws-test"}, "", "dev", "external"); err == nil {
		t.Fatal("A restricted session acquired another permission.")
	}
	forged := *id
	forged.VerbAllowlist = MaximumCollieAgentVerbs()
	if _, err := OperatorRequestSubmit(db, &forged, "request-3", args, "", "dev", "external"); err == nil {
		t.Fatal("A forged allowlist passed admission.")
	}
	if _, err := OperatorRequestSubmit(db, &RunIdentity{Tier: "pipeline", RunID: "worker"}, "request-4", args, "", "dev", "external"); err == nil {
		t.Fatal("A pipeline run submitted an operator request.")
	}
	if _, err := OperatorRequestClaim(db, id, first.ID); err == nil {
		t.Fatal("Collie claimed a host operation.")
	}
	claimed, err := OperatorRequestClaim(db, nil, first.ID)
	if err != nil || !claimed.Claimed {
		t.Fatalf("The host did not claim the request: %+v, %v", claimed, err)
	}
	second, err := OperatorRequestClaim(db, nil, first.ID)
	if err != nil || second.Claimed {
		t.Fatalf("A second executor claimed the same request: %+v, %v", second, err)
	}
}

func TestOperatorRequestsRejectAbsentTaskAndRunTargets(t *testing.T) {
	db := dvSpine(t)
	dvAddProject(t, db, "other-project", "active")
	id := &RunIdentity{Tier: "collie", RunID: "vision-future-targets", VerbAllowlist: MaximumCollieAgentVerbs()}
	permissions, err := json.Marshal(id.VerbAllowlist)
	if err != nil {
		t.Fatal(err)
	}
	dvExec(t, db, `INSERT INTO collie_sessions(id,vision_project_id,container_name,verb_allowlist,session_path,binding)
		VALUES(?,'ws-test','mc-collie-future-targets',?,'sessions/future-targets',?)`, id.RunID, string(permissions), dvCollieBinding(t))
	requests := []struct {
		id   string
		args []string
		body string
	}{
		{"future-task", []string{"task", "block", "50000", "--reason", "The operator requested a pause."}, ""},
		{"future-run", []string{"project", "role-unblock"}, `{"run":"future-run","reason":"The instructions were corrected."}`},
	}
	for _, request := range requests {
		if _, err := OperatorRequestSubmit(db, id, request.id, request.args, request.body, "dev", "collie"); err == nil {
			t.Fatalf("An absent resource acquired a request: %s", request.id)
		}
	}
	if count := dfInt(t, db, `SELECT COUNT(*) FROM operator_requests`); count != 0 {
		t.Fatal("An absent resource left an executable request.")
	}
	task := dvTask(50000, dispatch.ScopeTask, dispatch.StatusSeeded, 0)
	task.Project = "other-project"
	dvInsertTask(t, db, task)
	dvExec(t, db, `INSERT INTO runs(id,tier,role,project) VALUES('future-run','pipeline','strategist','other-project')`)
	for _, request := range requests {
		if _, err := OperatorRequestClaim(db, nil, request.id); err == nil {
			t.Fatalf("An old request executed after resource creation: %s", request.id)
		}
		if _, err := OperatorRequestSubmit(db, id, request.id, request.args, request.body, "dev", "collie"); err == nil || !strings.Contains(err.Error(), "another Project") {
			t.Fatalf("The new resource escaped its Project scope: %s %v", request.id, err)
		}
	}
	if count := dfInt(t, db, `SELECT COUNT(*) FROM operator_requests`); count != 0 {
		t.Fatal("The foreign resources acquired executable requests.")
	}
	if blocked := dfInt(t, db, `SELECT blocked FROM tasks WHERE id=50000`); blocked != 0 {
		t.Fatal("The refused request blocked the new task.")
	}
}

func TestOperatorRequestRefusalPreservesAnExecutionOwner(t *testing.T) {
	db := dvSpine(t)
	id := operatorSession(t, db, "refusal-owner", []string{"project.pause"})
	for _, request := range []string{"pending-refusal", "active-refusal", "uncertain-refusal"} {
		if _, err := OperatorRequestSubmit(db, id, request, []string{"project", "pause", "ws-test"}, "", "dev", "collie"); err != nil {
			t.Fatal(err)
		}
	}
	refusal := []byte(`{"error":{"code":"environment","message":"The helper was unavailable."}}`)
	refuse := func(request string) OperatorRequest {
		t.Helper()
		result, err := OperatorRequestRefuse(db, nil, request, 2, refusal, "The helper was unavailable.")
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if result := refuse("pending-refusal"); result.State != "complete" || *result.ExitCode != 2 {
		t.Fatalf("The pending refusal was lost: %+v", result)
	}
	for _, request := range []string{"active-refusal", "uncertain-refusal"} {
		if result, err := OperatorRequestClaim(db, nil, request); err != nil || !result.Claimed {
			t.Fatalf("The owner did not claim its request: %+v %v", result, err)
		}
	}
	if result := refuse("active-refusal"); result.State != "active" || result.ExitCode != nil {
		t.Fatalf("A failed claimant replaced the active owner: %+v", result)
	}
	if err := OperatorRequestRecover(db, nil, "uncertain-refusal"); err != nil {
		t.Fatal(err)
	}
	if result := refuse("uncertain-refusal"); result.State != "uncertain" {
		t.Fatalf("A failed claimant changed an uncertain effect: %+v", result)
	}
	completed := []byte(`{"status":"paused"}`)
	if err := OperatorRequestComplete(db, nil, "active-refusal", 0, completed, ""); err != nil {
		t.Fatal(err)
	}
	if result := refuse("active-refusal"); result.State != "complete" || *result.ExitCode != 0 || string(result.Result) != string(completed) {
		t.Fatalf("A failed claimant changed the owner's result: %+v", result)
	}
	if _, err := OperatorRequestRefuse(db, id, "pending-refusal", 2, refusal, ""); err == nil {
		t.Fatal("Collie acquired host refusal authority.")
	}
}

func TestOperatorRequestsRefuseInternalPayloads(t *testing.T) {
	db := dvSpine(t)
	id := operatorSession(t, db, "payloads", MaximumCollieAgentVerbs())
	for i, request := range []struct {
		args []string
		body string
	}{
		{[]string{"agent-profile", "credential-read"}, `{"id":"secret"}`},
		{[]string{"agent-profile", "operation-run"}, `{}`},
		{[]string{"agent-profile", "operation-start"}, `{"operation_id":"x","kind":"provider_auth","connection_id":"x"}`},
		{[]string{"environment", "retry"}, `{"project_id":"ws-test","runtime_binding":{"id":"forged"}}`},
		{[]string{"project", "pause", "ws-test", "--executable", "sh"}, ""},
	} {
		if _, err := OperatorRequestSubmit(db, id, string(rune('a'+i)), request.args, request.body, "dev", "collie"); err == nil {
			t.Fatalf("The host accepted an internal payload: %v", request.args)
		}
	}
	if got := dfInt(t, db, `SELECT COUNT(*) FROM operator_requests`); got != 0 {
		t.Fatalf("Rejected requests created %d receipts.", got)
	}
}

func TestOperatorRequestContextRechecksSessionBeforeTransaction(t *testing.T) {
	db := dvSpine(t)
	id := operatorSession(t, db, "context", []string{"project.pause"})
	if _, err := OperatorRequestSubmit(db, id, "context-op", []string{"project", "pause", "ws-test"}, "", "dev", "collie"); err != nil {
		t.Fatal(err)
	}
	if _, err := OperatorRequestClaim(db, nil, "context-op"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_RUN_JSON", filepath.Join(t.TempDir(), "absent"))
	code, err := WithOperatorRequest("context-op", func() int {
		if _, err := ProjectSetStatus(db, nil, "ws-test", "paused"); err != nil {
			t.Fatal(err)
		}
		return 0
	})
	if err != nil || code != 0 {
		t.Fatal(err)
	}
	if got := dfStr(t, db, `SELECT origin FROM operator_requests WHERE id='context-op'`); got != "collie" {
		t.Fatal(got)
	}
	dvExec(t, db, `UPDATE collie_sessions SET status='ended' WHERE id=?`, id.RunID)
	_, err = WithOperatorRequest("context-op", func() int {
		if _, err := ProjectSetStatus(db, nil, "ws-test", "active"); err == nil {
			t.Fatal("An ended session changed the Project through a helper.")
		}
		return 0
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := OperatorRequestComplete(db, nil, "context-op", 0, []byte(`{"status":"paused"}`), ""); err != nil {
		t.Fatal(err)
	}
	r, err := OperatorRequestGet(db, id, "context-op")
	if err != nil || r.State != "complete" {
		t.Fatalf("An ended session could not read its accepted result: %+v %v", r, err)
	}
	if _, err := OperatorRequestSubmit(db, id, "new-op", []string{"project", "pause", "ws-test"}, "", "dev", "collie"); err == nil {
		t.Fatal("An ended session submitted new work.")
	}
	identity, _ := json.Marshal(id)
	path := filepath.Join(t.TempDir(), "run.json")
	if err := os.WriteFile(path, identity, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MC_RUN_JSON", path)
	if _, err := WithOperatorRequest("context-op", func() int { return 0 }); err == nil {
		t.Fatal("An agent installed a host request context.")
	}
}

func TestOperatorRequestsRetainUncertainEffects(t *testing.T) {
	db := dvSpine(t)
	args := []string{"project", "pause", "ws-test"}
	if _, err := OperatorRequestSubmit(db, nil, "uncertain", args, "", "dev", "external"); err != nil {
		t.Fatal(err)
	}
	if _, err := OperatorRequestClaim(db, nil, "uncertain"); err != nil {
		t.Fatal(err)
	}
	if err := OperatorRequestRecover(db, nil, ""); err != nil {
		t.Fatal(err)
	}
	r, err := OperatorRequestSubmit(db, nil, "uncertain", args, "", "dev", "external")
	if err != nil || r.State != "uncertain" {
		t.Fatalf("Recovery lost the uncertain request: %+v %v", r, err)
	}
	r, err = OperatorRequestClaim(db, nil, "uncertain")
	if err != nil || r.Claimed {
		t.Fatalf("Recovery repeated an uncertain effect: %+v %v", r, err)
	}
	if err := OperatorRequestComplete(db, nil, "uncertain", 0, []byte(`{"status":"paused"}`), ""); err != nil {
		t.Fatal(err)
	}
	r, err = OperatorRequestGet(db, nil, "uncertain")
	if err != nil || r.State != "complete" {
		t.Fatalf("A late receipt was lost: %+v %v", r, err)
	}
}

func TestOperatorRequestsKeepConversationTargets(t *testing.T) {
	db := dvSpine(t)
	id := operatorSession(t, db, "own", MaximumCollieAgentVerbs())
	for _, args := range [][]string{
		{"collie", "title", "other", "--title", "Another conversation"},
		{"collie", "end", "other", "--reason", "The operator ended it."},
		{"collie", "draft", "other", "--to", "1", "--body", "A draft."},
	} {
		if _, err := OperatorRequestSubmit(db, id, "wrong-target", args, "", "dev", "collie"); err == nil {
			t.Fatalf("A request changed another conversation: %v", args)
		}
	}
	packet, version := hpPacket(t, db, 1)
	other, otherVersion := hpPacket(t, db, 2)
	started := hpMustPrepare(t, db, packet, version, false)
	packetID := &RunIdentity{Tier: "collie", RunID: started.SessionID, VerbAllowlist: MaximumCollieAgentVerbs()}
	args := []string{"packet", "decide", "2", "--packet", strconv.FormatInt(other, 10), "--version", strconv.FormatInt(otherVersion, 10), "--cancel", "--reason", "The operator cancelled it."}
	if _, err := OperatorRequestSubmit(db, packetID, "wrong-packet", args, "", "dev", "collie"); err == nil || !strings.Contains(err.Error(), "another packet") {
		t.Fatalf("Another packet escaped its conversation: %v", err)
	}
	dvAddProject(t, db, "other-project", "paused")
	permissions, _ := json.Marshal(MaximumCollieAgentVerbs())
	dvExec(t, db, `INSERT INTO collie_sessions(id,vision_project_id,container_name,verb_allowlist,session_path,binding) VALUES('vision-request','ws-test','mc-collie-vision',?,'sessions/vision',?)`, string(permissions), dvCollieBinding(t))
	visionID := &RunIdentity{Tier: "collie", RunID: "vision-request", VerbAllowlist: MaximumCollieAgentVerbs()}
	if _, err := OperatorRequestSubmit(db, visionID, "wrong-vision", []string{"project", "vision-edit"}, `{"id":"other-project","revision":"x","content":"Changed."}`, "dev", "collie"); err == nil {
		t.Fatal("A Vision request changed another Project.")
	}
}
