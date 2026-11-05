package verbs

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"mc/domain"
)

// CompleteArgs carries the flags of mc complete. One call selects one arm:
// --status worked|packaged, --checkpoint, --needs-operator, or --infra.
// The Scribe selects --commit with --wiki-evidence, or --infra.
type CompleteArgs struct {
	Affected []int64
	Task     int64
	Run      string
	Status   string // worked | packaged ("" for the flag terminals)
	// Commit is the exact HEAD commit of the isolated repository of the run.
	Commit  string
	Outputs string
	Thesis  string
	Reason  string // required by --needs-operator and --infra
	// WikiEvidence is the path of the OpenWiki completion receipt of a wiki update.
	WikiEvidence string
	// NeedsOperator records a task decision or a shared Project proposal decision.
	NeedsOperator bool
	// Infra reports a task run failure or a Project infrastructure failure.
	Infra      bool
	Checkpoint bool
}

// The bound the shipped Packager skill states. A rendered packet is prose and
// inlined media; past this it is not a decision-ready artifact, it is a leak.
const maxPacketRenderBytes = 64 << 20

// A correction is an argument, not an artifact: it says which criteria failed
// and what evidence says so. The bound is generous for prose and far below the
// packet's, because a correction that needs megabytes is a Verifier dumping
// logs where it owes a judgment.
const maxCorrectionBytes = 1 << 20

// Gate-ladder evidence sits between the two: it is prose mapping each
// criterion to what was actually observed, but every UI/UX criterion binds a
// VISUAL artifact, so a Playwright walk's evidence legitimately carries
// inlined images where a correction never would.
const maxEvidenceBytes = 16 << 20

// A deliverable that is not a repository commit — a report, an analysis, a
// rendered output — is the work itself, so it gets the packet's bound rather
// than prose's.
const maxDeliverableBytes = maxPacketRenderBytes

// A thesis is one paragraph the operator reads in thirty seconds. The
// bound is prose-shaped rather than generous: a thesis long enough to need
// kilobytes is the packet, and the packet is already the --outputs render.
const maxThesisBytes = 4096

// requireThesisProse keeps the queue card readable. It
// admits ordinary prose and refuses control characters, which would carry
// terminal escapes into two surfaces that render the value directly.
func requireThesisProse(thesis string) error {
	if len(thesis) > maxThesisBytes {
		return Usagef("--thesis is %d bytes; the 30-second thesis is at most %d", len(thesis), maxThesisBytes)
	}
	if !utf8.ValidString(thesis) {
		return Usagef("--thesis must be valid UTF-8")
	}
	for _, r := range thesis {
		if (r < 0x20 && r != '\n') || r == 0x7f {
			return Usagef("--thesis must not carry control characters; it is rendered directly on the queue card")
		}
	}
	if strings.TrimSpace(thesis) == "" {
		return Usagef("--thesis must not be blank")
	}
	return nil
}

// requirePackagedRender proves the review packet exists on a DURABLE root
// before the spine records a path to it.
//
// A Packager that finds nothing durable bound settles on container-local
// `/tmp`; the container is `--rm`, the file dies with it, and `mc packet
// list` goes on advertising a render nobody can read. Existence alone does
// not catch this: `mc` runs inside the same container, so `/tmp/...` stats
// fine. The load-bearing half is the root: the artifact plane is where
// durable outputs and rendered packets belong, and everything else the run
// can see dies with the container.
//
// The root alone is not enough either. `/mc/artifacts` is ordinary image
// rootfs: a Packager can write `/mc/artifacts/packet.md` at the class ROOT,
// pass every root check, and still lose the bytes with the `--rm` container.
//
// So membership is checked against the run's OWN declared planes rather than
// the shape of the path: `<root>/<bound-target>/x` and
// `<root>/<fabricated-target>/x` are indistinguishable as strings, and only
// the launch envelope knows which is a real host bind (RunIdentity.
// ArtifactPlanes). A path a terminal records is one the terminal proved
// durable.
func requirePackagedRender(id *RunIdentity, task int64, outputs string) error {
	return requireDurableRunArtifact(id, durableArtifactCheck{
		Flag:    "mc complete --status packaged --outputs",
		Path:    outputs,
		Max:     maxPacketRenderBytes,
		Why:     "so the packet would not survive to be reviewed",
		Empty:   "an unwritten render is not a review packet",
		Gone:    "the render never landed, so the packet would be born pointing at nothing",
		Bound:   "packet",
		Leaf:    "packet",
		Subject: task,
	}, id.ArtifactPlanes())
}

// requireDurableOutputs requires a durable artifact for each output path.
func requireDurableOutputs(id *RunIdentity, task int64, status, outputs string) error {
	if outputs == "" {
		return nil
	}
	switch status {
	case "packaged":
		return requirePackagedRender(id, task, outputs)
	case "worked":
		return requireDurableRunArtifact(id, durableArtifactCheck{
			Flag:    "mc complete --status worked --outputs",
			Path:    outputs,
			Max:     maxDeliverableBytes,
			Why:     "the deliverable must survive the container",
			Empty:   "an empty file is not a deliverable and not a completion report",
			Gone:    "the output never landed, so the next stage would be handed a path to nothing",
			Bound:   "deliverable",
			Leaf:    outputLeafForRole(id),
			Subject: task,
		}, id.ArtifactPlanes())
	}
	return nil
}

// durableArtifactCheck parameterizes the one check every terminal shares:
// A PATH A TERMINAL RECORDS IS OPENED BY THE TERMINAL. Only the wording varies
// per terminal, so only the wording is a field.
type durableArtifactCheck struct {
	Flag  string // the exact invocation prefix, quoted back to the agent
	Path  string
	Max   int64
	Why   string // what is lost if the bytes die with the container
	Empty string // why an empty file is not a valid record
	Gone  string // why a missing file is not a valid record
	Bound string // what the size bound is protecting
	// Leaf and Subject build the destination the refusal SUGGESTS. Without
	// them a refusal states only what is wrong: the common branch — a role
	// that wrote to /tmp, /mc/scratch, or its own source checkout — named the
	// class root, which is the one destination the next attempt is also
	// refused for. A fence that leaves no reachable next move strands the role
	// instead of correcting it, and a stranded role takes no terminal at all.
	// Leaf is the per-task folder for this artifact class; Subject is the task
	// id it hangs under, 0 to omit the id segment.
	Leaf    string
	Subject int64
}

// durableDestinationHint turns the run's own declared planes into a
// copy-pasteable place to write. The planes come from the read-only run.json,
// so this discloses nothing the run's mount plan does not already give it.
func durableDestinationHint(c durableArtifactCheck, planes []string) string {
	if len(planes) == 0 {
		return "this run was launched with no writable artifact plane at all, so nothing it wrote survives its container. Report --infra rather than recording a path nobody can read"
	}
	// planes[0] is deterministic (mount-request order) but arbitrary when a
	// profile declares several roots, which is why the full list is printed
	// beside it rather than instead of it.
	example := planes[0]
	if c.Subject > 0 {
		example = filepath.Join(example, strconv.FormatInt(c.Subject, 10))
	}
	if c.Leaf != "" {
		example = filepath.Join(example, c.Leaf)
	}
	// `<your-file>` rather than a complete filename: an example that reads as
	// an existing path invites a role to record it without writing it, which is
	// the next refusal down.
	return fmt.Sprintf(
		"write it inside one of this run's mounted artifact planes (%s) — for example %s/<your-file> — and pass that absolute path",
		strings.Join(planes, ", "), example)
}

// requireDurableRunArtifact proves a recorded path names bytes that outlive the
// run: under the artifact root, inside one of THIS run's own mounted planes
// (read from the read-only run.json, so it is unforgeable), and actually there.
//
// The plane check is the load-bearing half and the reason existence alone is
// worthless here. `mc` runs inside the same container as the agent, so a
// container-local scratch path stats perfectly well and proves nothing about
// survival — that is exactly how a Packager once wrote at the artifact ROOT,
// passed every check, and had its bytes die with the `--rm` container.
func requireDurableRunArtifact(id *RunIdentity, c durableArtifactCheck, planes []string) error {
	root := ArtifactsMountRoot()
	clean := filepath.Clean(c.Path)
	if !filepath.IsAbs(clean) || (clean != root && !strings.HasPrefix(clean, root+string(filepath.Separator))) {
		return Domainf("%s %q is not under the durable artifact root %s: everything else this run can write is container-local scratch that dies with it, %s: %s", c.Flag, c.Path, root, c.Why, durableDestinationHint(c, planes))
	}
	if len(planes) == 0 {
		return Domainf("%s %q cannot be durable: %s", c.Flag, c.Path, durableDestinationHint(c, planes))
	}
	onPlane := false
	for _, plane := range planes {
		if clean == plane || strings.HasPrefix(clean, plane+string(filepath.Separator)) {
			onPlane = true
			break
		}
	}
	if !onPlane {
		return Domainf("%s %q is under %s but not inside one of this run's mounted artifact planes (%s): %s itself is container-local image storage, so a file written directly there is writable, passes every other check, and still dies with the run. Rewrite it inside a listed plane: %s", c.Flag, c.Path, root, strings.Join(planes, ", "), root, durableDestinationHint(c, planes))
	}
	// Lstat, never Stat: a symlink out of the artifact root is not durable,
	// however well it resolves from inside this container.
	info, err := os.Lstat(clean)
	if os.IsNotExist(err) {
		return Domainf("%s %q does not exist: %s: %s", c.Flag, c.Path, c.Gone, durableDestinationHint(c, planes))
	}
	if err != nil {
		return Domainf("%s cannot read %q: %v", c.Flag, c.Path, err)
	}
	if !info.Mode().IsRegular() {
		return Domainf("%s %q is not a regular file (mode %s)", c.Flag, c.Path, info.Mode())
	}
	if info.Size() == 0 {
		return Domainf("%s %q is empty; %s", c.Flag, c.Path, c.Empty)
	}
	if info.Size() > c.Max {
		return Domainf("%s %q is %d bytes, over the %d-byte %s bound", c.Flag, c.Path, info.Size(), c.Max, c.Bound)
	}
	return nil
}

// Complete records the result and releases the lease in one transaction.
// The run of a wiki update holds no lease and has its own terminal.
func Complete(db *sql.DB, id *RunIdentity, a CompleteArgs) (any, error) {
	if id != nil && id.Role == "scribe" {
		return completeWikiUpdate(db, id, a)
	}
	if a.WikiEvidence != "" {
		return nil, Usagef("mc complete accepts --wiki-evidence from the Scribe only")
	}
	if a.Status != "" || a.Checkpoint {
		if a.Checkpoint {
			if a.Status != "" {
				return nil, Usagef("the checkpoint requires its own terminal")
			}
			a.Status = "worked"
		}
		if a.NeedsOperator || a.Infra {
			return nil, Usagef("mc complete requires exactly one of --status, --checkpoint, --needs-operator, --infra")
		}
		return completeDelivery(db, id, a)
	}
	if a.NeedsOperator == a.Infra {
		return nil, Usagef("mc complete requires exactly one of --status, --checkpoint, --needs-operator, --infra")
	}
	if err := requirePipeline(id); err != nil {
		return nil, err
	}
	if a.Run == "" {
		return nil, Usagef("mc complete requires --run")
	}
	if err := requireOwnRun(id, a.Run); err != nil {
		return nil, err
	}
	if a.Reason == "" {
		return nil, Usagef("mc complete --needs-operator/--infra require --reason")
	}
	if a.Outputs != "" || a.Commit != "" || a.Thesis != "" {
		return nil, Usagef("--needs-operator/--infra take only --reason")
	}
	if a.Infra {
		var subject sql.NullInt64
		if err := db.QueryRow(`SELECT subject FROM runs WHERE id = ?`, a.Run).Scan(&subject); err != nil {
			return nil, classify(err)
		}
		if subject.Valid && subject.Int64 != a.Task || !subject.Valid && a.Task != 0 {
			return nil, Domainf("task %d is not the run's assigned task", a.Task)
		}
		return RunInfrastructureFailure(db, id, a.Run, domain.InfrastructureFailureReport{Condition: a.Reason, Source: "agent"})
	}

	result := map[string]any{"task_id": a.Task}
	err := inTx(db, func(ctx context.Context, q Q) error {
		subject, err := fenceRun(ctx, q, a.Run)
		if err != nil {
			return err
		}
		if subject == nil {
			if a.Task != 0 {
				return Domainf("The proposal run has no task subject.")
			}
			decision, err := domain.RecordProposalDecision(ctx, q, a.Run, a.Reason, a.Affected)
			if err != nil {
				return err
			}
			result["proposal_decision_id"] = decision
			result["blocked"] = true
			return endRun(ctx, q, a.Run, "blocked")
		}
		if *subject != a.Task {
			return Domainf("task %d is not the live lease's subject", a.Task)
		}
		pending, err := domain.RunRequirementsSuperseded(ctx, q, a.Run, a.Task)
		if err != nil {
			return err
		}
		if pending {
			if _, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES(?,'task.superseded_block',?,?)`, id.Role, a.Task, a.Reason); err != nil {
				return err
			}
			result["superseded"] = true
		} else {
			if err := blockTaskTx(ctx, q, a.Task, domain.BlockReport, a.Reason, id.Role); err != nil {
				return err
			}
			result["blocked"] = true
		}
		if err := endRun(ctx, q, a.Run, "blocked"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// outputLeafForRole names the output directory for a task.
func outputLeafForRole(id *RunIdentity) string {
	return "output"
}
