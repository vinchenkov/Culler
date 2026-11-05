package verbs

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"mc/dispatch"
)

const (
	resolvedRequirements = "The export includes the date. The export check runs outside the agent container."
	resolutionReason     = "The agent containers have no Docker daemon, so the operator moved the export check outside the task."
)

// resolvedTask is a task whose operator decision report the operator
// resolved with a requirements revision.
type resolvedTask struct {
	f         *deliveryFixture
	task      int64
	base      string
	submitted string
}

// resolveVerifierReport builds a task through a Verifier correction, a second
// Worker submission, and a Verifier report on that submission. The operator
// then resolves the report with a requirements revision.
func resolveVerifierReport(t *testing.T) resolvedTask {
	t.Helper()
	f := newDeliveryFixture(t)
	r := resolvedTask{f: f, base: srcGit(t, f.workspace, "rev-parse", "main"), task: f.add("Export the report")}
	f.advance(r.task, "export.txt", "The export.\n", "worked")
	submitVerdict(t, f, r.task, "correct")
	run, root, id := f.run(r.task, "worker")
	r.submitted = f.commit(root, "export.txt", "The corrected export.\n")
	if _, err := Complete(f.db, id, CompleteArgs{Task: r.task, Run: run, Status: "worked", Commit: r.submitted}); err != nil {
		t.Fatal(err)
	}
	f.capture(r.task, run, root)
	run, root, id = f.run(r.task, "verifier")
	if _, err := Complete(f.db, id, CompleteArgs{Task: r.task, Run: run, NeedsOperator: true, Reason: "The export check needs a Docker daemon. Select the required evidence."}); err != nil {
		t.Fatal(err)
	}
	f.stopped(r.task, run, root)
	result := boardRequest(t, f.db, BoardWriteRequest{OperationID: "resolve-report", Action: "revise", TaskID: r.task,
		ExpectedVersion: boardVersion(t, f.db, r.task), Description: boardText(resolvedRequirements), Reason: boardText(resolutionReason)})
	if result["revision_state"] != "applied" {
		t.Fatalf("the revision did not apply: %v", result)
	}
	return r
}

// brief builds and renders the run brief of the role for the next run, then
// reads it back the way the agent reads it.
func (r resolvedTask) brief(t *testing.T, role dispatch.Role) (renderedBrief, string) {
	t.Helper()
	repo, err := loadRepositoryProjection(context.Background(), r.f.db, r.task)
	if err != nil {
		t.Fatal(err)
	}
	text, err := renderRunBrief(briefFor(t, r.f.db, role, &r.task, briefInputs{Repository: repo}))
	if err != nil {
		t.Fatal(err)
	}
	return parseRenderedBrief(t, text), text
}

// resubmit runs one Worker that submits its start commit unchanged and returns
// that commit.
func (r resolvedTask) resubmit(t *testing.T) string {
	t.Helper()
	run, root, id := r.f.run(r.task, "worker")
	start := srcGit(t, filepath.Join(root, "repo"), "rev-parse", "HEAD")
	if _, err := Complete(r.f.db, id, CompleteArgs{Task: r.task, Run: run, Status: "worked", Commit: start}); err != nil {
		t.Fatal(err)
	}
	r.f.capture(r.task, run, root)
	return start
}

func requireRequirementsRevisionPart(t *testing.T, brief renderedBrief, version string) {
	t.Helper()
	part := brief.part(partRequirementsRevision)
	if part == nil {
		t.Fatalf("the Worker brief holds no %s part: %v", partRequirementsRevision, brief.headings())
	}
	if part.Text != resolutionReason || part.Facts["Requirements version"] != version {
		t.Fatalf("the %s part holds %q at the requirements version %q", partRequirementsRevision, part.Text, part.Facts["Requirements version"])
	}
	if part.Explanation != "This is the operator's reason for the requirements revision." {
		t.Fatalf("the %s part explanation is %q", partRequirementsRevision, part.Explanation)
	}
	requireAgentText(t, "requirements procedure", roleSkillBody(t, dispatch.RoleWorker, "mc-worker"), []string{
		"The Task description holds every requirement; the Requirements revision reason supplies context, not additional requirements.",
	}, nil)
}

// The dispatched Worker brief after a resolution holds the revised
// description and the reason in a separate part. The correction of the
// earlier requirements version stays out.
func TestWorkerBriefAfterAResolutionHoldsTheReasonAsContext(t *testing.T) {
	r := resolveVerifierReport(t)
	value, err := dvDispatchOne(r.f.db)
	if err != nil {
		t.Fatal(err)
	}
	effect := value.(map[string]any)
	if effect["action"] != "spawn" || effect["role"] != "worker" {
		t.Fatalf("dispatch did not start the Worker: %v", effect)
	}
	text := fmt.Sprint(effect["brief"])
	brief := parseRenderedBrief(t, text)
	if !strings.Contains(brief.Header, "mc.spawn-brief.v23") {
		t.Fatalf("the brief names the schema %q", brief.Header)
	}
	if task := brief.part(partTask); task == nil || task.Text != resolvedRequirements {
		t.Fatalf("the Worker brief holds the description %+v", task)
	}
	requireRequirementsRevisionPart(t, brief, "2")
	for _, absent := range []string{partCorrection, partRevision} {
		if brief.part(absent) != nil {
			t.Fatalf("the Worker brief holds the %s part:\n%s", absent, text)
		}
	}
	if strings.Count(text, resolutionReason) != 1 {
		t.Fatalf("the reason occurs outside the %s part:\n%s", partRequirementsRevision, text)
	}
	git := brief.part(partGitState)
	if git == nil || git.Facts["Start commit"] != r.submitted || git.Facts["Base commit"] != r.base {
		t.Fatalf("the Worker Git state is %+v, want the start commit %s and the base commit %s", git, r.submitted, r.base)
	}
}

// Each Worker run on the requirements version of the revision receives the
// reason, also beside a correction on that version.
func TestLaterWorkerRunOnTheRevisedRequirementsKeepsTheReason(t *testing.T) {
	r := resolveVerifierReport(t)
	r.resubmit(t)
	submitVerdict(t, r.f, r.task, "correct")
	requireNextRole(t, r.f, dispatch.RoleWorker)
	brief, _ := r.brief(t, dispatch.RoleWorker)
	requireRequirementsRevisionPart(t, brief, "2")
	correction := brief.part(partCorrection)
	if correction == nil || correction.Facts["Requirements version"] != "2" {
		t.Fatalf("the Worker brief holds the correction %+v, want the correction on requirements version 2", correction)
	}
}

// The Verifier and the Packager judge the revised description. The reason
// reaches neither role, and the Verifier judges the change from the base
// commit of the first Worker run.
func TestVerifierAndPackagerBriefsAfterAResolutionHoldNoReason(t *testing.T) {
	r := resolveVerifierReport(t)
	if resubmitted := r.resubmit(t); resubmitted != r.submitted {
		t.Fatalf("the Worker started at %s, want the last submitted commit %s", resubmitted, r.submitted)
	}
	verifier, text := r.brief(t, dispatch.RoleVerifier)
	git := verifier.part(partGitState)
	if git == nil || git.Facts["Base commit"] != r.base || git.Facts["Submitted commit"] != r.submitted {
		t.Fatalf("the Verifier Git state is %+v, want the base commit %s and the submitted commit %s", git, r.base, r.submitted)
	}
	requireRevisedDescriptionWithoutReason(t, dispatch.RoleVerifier, verifier, text)

	submitVerdict(t, r.f, r.task, "pass")
	packager, text := r.brief(t, dispatch.RolePackager)
	requireRevisedDescriptionWithoutReason(t, dispatch.RolePackager, packager, text)
	if verdict := packager.part(partLatestVerdict); verdict == nil || verdict.Facts["Outcome"] != "pass" || verdict.Facts["Requirements version"] != "2" {
		t.Fatalf("the Packager brief holds the verdict %+v", verdict)
	}
}

func requireRevisedDescriptionWithoutReason(t *testing.T, role dispatch.Role, brief renderedBrief, text string) {
	t.Helper()
	if task := brief.part(partTask); task == nil || task.Text != resolvedRequirements {
		t.Fatalf("the %s brief holds the description %+v", role, task)
	}
	if brief.part(partRequirementsRevision) != nil || strings.Contains(text, resolutionReason) {
		t.Fatalf("the %s brief holds the reason of the revision:\n%s", role, text)
	}
}

// A packet revision returns the work with a request that the Worker must meet.
func TestPacketRevisionRequestBindsTheWorker(t *testing.T) {
	db := dvSpine(t)
	task := dvTask(1, dispatch.ScopeTask, dispatch.StatusPackaged, 2)
	dvInsertTask(t, db, task)
	packet, version := dvPublishPacket(t, db, 1, "packet.html", dvOld)
	const request = "Add the date to the export header."
	if _, err := PacketDecide(db, nil, 1, "revise", request, packet, version); err != nil {
		t.Fatal(err)
	}
	brief := briefFor(t, db, dispatch.RoleWorker, &task.ID, briefInputs{})
	part := brief.part(partRevision)
	if part == nil || part.Text != request {
		t.Fatalf("the Worker brief holds the revision request %+v", part)
	}
	if part.Explanation != "This is the operator's request for this revision." {
		t.Fatalf("the %s part explanation is %q", partRevision, part.Explanation)
	}
	if brief.part(partRequirementsRevision) != nil {
		t.Fatalf("a packet revision gave the Worker a %s part", partRequirementsRevision)
	}
}
