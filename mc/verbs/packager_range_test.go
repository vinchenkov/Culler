package verbs

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mc/dispatch"
	"mc/repository"
)

// Execute the command from its delivered skill against the test repository.
func packagerGit(t *testing.T, repo, skillName, command string, values map[string]string) (string, error) {
	t.Helper()
	skills, err := RoleSkillSet(dispatch.RolePackager)
	if err != nil {
		t.Fatal(err)
	}
	line := "git -C /workspace/source " + command
	var found bool
	for _, skill := range skills {
		found = found || (skill.Name == skillName && strings.Contains(skill.Body, line))
	}
	if !found {
		t.Fatalf("the %s skill omits the command %q", skillName, line)
	}
	for name, value := range values {
		command = strings.ReplaceAll(command, "<"+name+">", value)
	}
	cmd := exec.Command("git", strings.Fields(command)...)
	cmd.Dir = repo
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func TestRunRepositoryNamesTheDestinationCommit(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Name the destination commit")
	_, root, _ := f.run(task, "worker")
	main := srcGit(t, f.workspace, "rev-parse", "main")
	if got := srcGit(t, filepath.Join(root, "repo"), "rev-parse", repository.DestinationRef); got != main {
		t.Fatalf("the run repository names %s as the destination commit, want %s", got, main)
	}
}

// The operator approves a packet, and the destination branch then gets a
// conflicting change. The Packager of the repaired work reads only the change
// of the work, and Git shows the difference from the approved packet.
func TestPackagerAfterAMergeRepairReadsTheWorkAndItsDifference(t *testing.T) {
	f := newDeliveryFixture(t)
	base := srcGit(t, f.workspace, "rev-parse", "main")
	task := f.add("Preserve the required result")
	first := f.pass(task, "answer.txt", "worker 0\n")
	if briefFor(t, f.db, dispatch.RolePackager, &task, briefInputs{}).part(partPreviousPacket) != nil {
		t.Fatal("the first packet of the work has a previous packet")
	}
	f.merge(task, "prepare")
	f.approve(task)
	f.commitMain("other.txt", "another accepted contribution\n")
	destination := f.commitMain("answer.txt", "main 0\n")
	f.merge(task, "apply")
	if candidate := f.merge(task, "prepare"); candidate.Status != "conflict" {
		t.Fatalf("the controlled Git conflict did not occur: %+v", candidate)
	}
	second := f.pass(task, "answer.txt", "worker 1\n")

	brief := briefFor(t, f.db, dispatch.RolePackager, &task, briefInputs{})
	packet := dfStr(t, f.db, `SELECT p.render_path FROM review_versions p JOIN task_versions v ON v.id=p.version_id WHERE p.task_id=? AND v.result_commit=?`, task, first)
	if previous := brief.part(partPreviousPacket); previous == nil || previous.fact("File") != packet || previous.fact("Submitted commit") != first {
		t.Fatalf("the Packager brief omitted the approved packet: %+v", previous)
	}
	if state := brief.part(partGitState); state == nil || state.fact("Submitted commit") != second || state.fact("Base commit") != "" {
		t.Fatalf("the Packager brief holds the wrong range facts: %+v", state)
	}

	root := filepath.Join(f.root, "packager-range")
	if _, err := executeRepository(context.Background(), RepositoryEnvelope{Operation: "ready", TaskID: task, RunID: "packager-range", WorkspaceRoot: f.workspace, RunRoot: root, InputCommit: second, DestinationBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repo")
	if names := srcGit(t, repo, "diff", "--name-only", base, second); !strings.Contains(names, "other.txt") {
		t.Fatalf("the fixture has no other contribution after the base commit: %q", names)
	}

	start, err := packagerGit(t, repo, "mc-packager", "merge-base refs/mc/destination <Submitted commit>", map[string]string{"Submitted commit": second})
	if err != nil || start != destination {
		t.Fatalf("the reviewed range starts at %s, %v; want the merged destination commit %s", start, err, destination)
	}
	reviewed, err := packagerGit(t, repo, "mc-packager", "diff <range start> <Submitted commit>", map[string]string{"range start": start, "Submitted commit": second})
	if err != nil || !strings.Contains(reviewed, "+worker 1") || strings.Contains(reviewed, "other.txt") {
		t.Fatalf("the reviewed change is not the change of the work alone: %v\n%s", err, reviewed)
	}

	previousStart, err := packagerGit(t, repo, "packager-previous-packet", "merge-base refs/mc/destination <previous commit>", map[string]string{"previous commit": first})
	if err != nil || previousStart != base {
		t.Fatalf("the previous reviewed range starts at %s, %v; want %s", previousStart, err, base)
	}
	applied, err := packagerGit(t, repo, "packager-previous-packet", "merge-tree --write-tree --merge-base=<previous start> <previous commit> <range start>",
		map[string]string{"previous start": previousStart, "previous commit": first, "range start": start})
	if exit, ok := err.(*exec.ExitError); err != nil && (!ok || exit.ExitCode() != 1) {
		t.Fatalf("the previous reviewed change did not apply: %v", err)
	}
	tree, _, _ := strings.Cut(applied, "\n")
	difference, err := packagerGit(t, repo, "packager-previous-packet", "diff <tree> <Submitted commit>", map[string]string{"tree": tree, "Submitted commit": second})
	if err != nil || !strings.Contains(difference, "-<<<<<<<") || !strings.Contains(difference, "+worker 1") || strings.Contains(difference, "other.txt") {
		t.Fatalf("the difference from the previous packet is not the conflict resolution alone: %v\n%s", err, difference)
	}
}

func TestPackagerBriefNamesThePreviousPacketAfterARevision(t *testing.T) {
	f := newDeliveryFixture(t)
	task := f.add("Deliver the original requirements")
	first := f.pass(task, "feature.txt", "The original result.\n")
	f.merge(task, "prepare")
	if briefFor(t, f.db, dispatch.RolePackager, &task, briefInputs{}).part(partPreviousPacket) != nil {
		t.Fatal("the packet of the current submitted commit is a previous packet")
	}
	boardRevision(t, f.db, task, "Revised requirements")
	second := f.pass(task, "feature.txt", "The revised result.\n")
	previous := briefFor(t, f.db, dispatch.RolePackager, &task, briefInputs{}).part(partPreviousPacket)
	if previous == nil || previous.fact("Submitted commit") != first {
		t.Fatalf("the Packager brief omitted the packet of the original requirements: %+v", previous)
	}
	// The repository of the new run holds the previous submitted commit, so Git can show the difference.
	if !(repository.Git{Root: f.workspace}).Ancestor(context.Background(), first, second) {
		t.Fatal("the revised work does not hold the previous submitted commit")
	}
}
