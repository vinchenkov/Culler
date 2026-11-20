package operatorcmd

import (
	"slices"
	"strings"
	"testing"
)

// The operator command routing table is the entry list the G-F coverage
// matrix (docs/facts/g-f-coverage-matrix.md) classifies, and the F1 P2
// deliverable. A new or changed command must be reclassified there before
// it can pass here.

const a12RouteTable = `task get|false|false|
task revision|false|false|
task list|false|false|
task block|false|true|task.block
task unblock|false|true|task.unblock
task resume|false|true|task.resume
task cancel|false|true|task.cancel
task interrupt|false|true|task.interrupt
board write|false|true|board.write
board history-report|false|false|
packet list|false|false|
packet decide|false|true|packet.decide
project list|false|false|
project pause|false|true|project.pause
project resume|false|true|project.resume
project archive|false|true|project.archive
project delete-preview|true|false|
project delete|true|true|project.delete
project workspaces|true|false|
project readiness|true|false|
project setup-preview|true|false|
project setup-save|true|true|project.setup-save
project details|true|false|
project vision-save|true|true|project.vision-save
project vision-open|true|true|project.vision-open
project vision-workspace|true|false|
project vision-apply|true|true|project.vision-apply
project start|true|true|project.start
project vision-draft|false|false|project.vision-draft
project vision-edit|false|true|project.vision-edit
project vision-rebase|false|true|project.vision-rebase
project vision-submit|false|true|project.vision-submit
project vision-submission|false|false|project.vision-submission
project role-show|false|false|
project role-save|false|true|project.role-save
project role-unblock|false|true|project.role-unblock
run list|false|false|
run customization|false|false|
activity list|false|false|
pause status|false|false|
pause|false|true|pause
resume|false|true|resume
environment get|false|false|environment.get
environment save|true|true|environment.save
environment approve|false|true|environment.approve
environment deny|false|true|environment.deny
environment assist|false|true|environment.assist
environment retry|true|true|environment.retry
environment cache-clear|false|true|environment.cache-clear
system status|true|false|
system diagnostics|true|false|
system resume-infrastructure|true|true|system.resume-infrastructure
system resolve-proposals|true|true|system.resolve-proposals
connection check-start|true|true|connection.check-start
connection retry|true|true|connection.retry
connection check-cancel|true|true|connection.check-cancel
config show|true|false|
config save|true|true|config.save
agent-profile list|true|false|
agent-profile models|true|false|
agent-profile save|true|true|agent-profile.save
agent-profile operation-start|true|true|agent-profile.operation-start
agent-profile operation-status|true|false|
agent-profile operation-cancel|true|true|agent-profile.operation-cancel
routing show|true|false|
routing set|true|true|routing.set
logs resident|true|false|
logs dashboard|true|false|
logs agentsview|true|false|
logs mc|true|false|
logs run|true|false|
logs runs|true|false|
health|true|false|
collie title|false|true|collie.title
collie draft|false|true|collie.draft
collie history|false|false|collie.history
collie list|false|false|collie.list
collie end|false|true|collie.end
collie packet-context|false|false|
operation get|false|false|
operation list|false|false|`

func a12RenderTable() string {
	var lines []string
	for _, d := range Definitions() {
		lines = append(lines, d.Name()+"|"+boolWord(d.Host)+"|"+boolWord(d.Mutates)+"|"+d.Permission)
	}
	return strings.Join(lines, "\n")
}

func boolWord(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func TestOperatorRoutingTableMatchesGFCoverageMatrix(t *testing.T) {
	if got := a12RenderTable(); got != a12RouteTable {
		t.Fatalf("the operator routing table drifted from the G-F matrix:\n%s", got)
	}
}

func TestOperatorRoutingTableClassificationInvariants(t *testing.T) {
	definitions := Definitions()
	if len(definitions) != 81 {
		t.Fatalf("route table size = %d, want 81", len(definitions))
	}
	seen := map[string]bool{}
	spineWrites, spineReads, hostLocal := 0, 0, 0
	for _, d := range definitions {
		name := d.Name()
		if seen[name] {
			t.Fatalf("duplicate command path %q", name)
		}
		seen[name] = true
		if d.Mutates && d.Permission == "" {
			t.Fatalf("%s: a mutation without a permission name escapes the fence surface", name)
		}
		if found, ok := Lookup(d.Path); !ok || !slices.Equal(found.Path, d.Path) {
			t.Fatalf("%s: Lookup lost its own definition", name)
		}
		switch {
		case d.Host:
			hostLocal++
		case d.Mutates:
			spineWrites++
		default:
			spineReads++
		}
	}
	if spineWrites != 24 || spineReads != 19 || hostLocal != 38 {
		t.Fatalf("classification drifted: spine writes %d (want 24), spine reads %d (want 19), host-local %d (want 38)", spineWrites, spineReads, hostLocal)
	}
}
