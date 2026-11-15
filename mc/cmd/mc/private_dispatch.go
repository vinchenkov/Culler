package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"mc/substrate"
	"mc/verbs"
)

const maxPrivateFrameBytes = 1 << 20

// maxHelperStderrBytes bounds the helper diagnostic carried back on a broker
// failure. It is a diagnostic tail, not a channel: enough for an exit status
// and a refusal line, far too small to be used to move data.
const maxHelperStderrBytes = 2 << 10

var (
	privateHelperSelfTimeout = 4 * time.Second
	helperWallTimeout        = 120 * time.Second
	helperNoProgressTimeout  = 5 * time.Second
)

const privateHelperTimeoutExitCode = 124

// helperDeadlineBudget converts the broker's absolute deadline into this
// helper's own budget. Every private helper reads it through here, so the
// policy lives in exactly one place.
//
// The Darwin broker mints the instant from the HOST clock; the helper reads it
// back against the Linux VM clock, and nothing makes the two agree. The VM is
// paused through host sleep, so on dark-wake it trails the host until timesync
// reconverges. A refusal of an over-long deadline turns that clock skew into a
// usage refusal, and the resident's attachment sweep aborts its whole tick
// when a leg refuses, so dispatch stops.
//
// Clamping is strictly stronger than refusing. The guard's only obligation is
// that a helper never holds the spine longer than the broker allows; a capped
// budget enforces that. The already-expired case keeps its own answer:
// ok=false, never start work whose deadline has passed.
func helperDeadlineBudget(deadlineAt, now time.Time) (budget time.Duration, ok bool) {
	remaining := deadlineAt.Sub(now)
	if remaining <= 0 {
		return 0, false
	}
	if remaining > privateHelperSelfTimeout {
		return privateHelperSelfTimeout, true
	}
	return remaining, true
}

func runPrivateDispatch(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if !privateHelperScopeOK() {
		fmt.Fprintln(stderr, "mc: private dispatch verb requires the helper's fixed spine scope")
		return 1
	}
	if len(args) != 3 || args[1] != "--deadline-unix-ms" {
		fmt.Fprintln(stderr, "mc: private dispatch verb requires its broker deadline")
		return 2
	}
	deadlineMillis, err := strconv.ParseInt(args[2], 10, 64)
	if err != nil {
		fmt.Fprintln(stderr, "mc: private dispatch broker deadline is invalid")
		return 2
	}
	now := time.Now()
	budget, ok := helperDeadlineBudget(time.UnixMilli(deadlineMillis), now)
	if !ok {
		os.Exit(privateHelperTimeoutExitCode)
	}
	// The CLAMPED instant goes down, not the broker's raw one:
	// runPrivateDispatchInScope recomputes time.Until against whatever it is
	// handed, so re-deriving it here is what keeps the cap honest.
	return runPrivateDispatchInScope(args[0], now.Add(budget), stdin, stdout, stderr)
}

func runPrivateDispatchInScope(verb string, deadlineAt time.Time, stdin io.Reader, stdout, stderr io.Writer) int {
	// This timer lives in the container-side mc process, not the Darwin-side
	// docker CLI. It fires before the broker's no-progress deadline, so the
	// broker cannot report a timeout while a detached helper still holds the
	// flock or remains capable of committing.
	remaining := time.Until(deadlineAt)
	if remaining <= 0 {
		os.Exit(privateHelperTimeoutExitCode)
	}
	deadline := time.AfterFunc(remaining, func() {
		os.Exit(privateHelperTimeoutExitCode)
	})
	defer deadline.Stop()

	switch verb {
	case "__dispatch-plan":
		var request verbs.PrivateDispatchPlanRequest
		if err := readPrivateFrame(stdin, &request); err != nil {
			fmt.Fprintln(stderr, "mc: invalid private plan frame")
			return 1
		}
		if err := validatePrivateReleaseIdentity(request.ReleaseBuildID, request.ControlVersion,
			request.SpineSchemaID, request.ConfigSchemaVersion); err != nil {
			fmt.Fprintln(stderr, "mc:", err)
			return 1
		}
		out, err := withSpine(func(db *sql.DB) (any, error) {
			return verbs.DispatchPlanPrivateDB(db, request)
		})
		if err != nil {
			fmt.Fprintln(stderr, "mc: private plan refused:", err)
			return 1
		}
		if err := writePrivateFrame(stdout, out); err != nil {
			fmt.Fprintln(stderr, "mc: write private plan frame:", err)
			return 2
		}
		return 0
	case "__dispatch-prepare":
		var request verbs.PrivateDispatchPrepareRequest
		if err := readPrivateFrame(stdin, &request); err != nil {
			fmt.Fprintln(stderr, "mc: invalid private prepare frame")
			return 1
		}
		if err := validatePrivateReleaseIdentity(request.ReleaseBuildID, request.ControlVersion,
			request.SpineSchemaID, request.ConfigSchemaVersion); err != nil {
			fmt.Fprintln(stderr, "mc:", err)
			return 1
		}
		out, err := withSpine(func(db *sql.DB) (any, error) {
			return verbs.DispatchPreparePrivateDB(db, request)
		})
		if err != nil {
			// The reason travels with the refusal. The broker relays the
			// helper's stderr into the resident log (helperReason), and a bare
			// "refused" there is undiagnosable. This channel is operator-plane:
			// the resident invokes `mc dispatch`, never an agent, so the detail
			// stays out of the agent-facing refusals.
			fmt.Fprintln(stderr, "mc: private prepare refused:", err)
			return 1
		}
		if err := writePrivateFrame(stdout, out); err != nil {
			fmt.Fprintln(stderr, "mc: write private prepare frame:", err)
			return 2
		}
		return 0
	case "__dispatch-commit":
		var request verbs.PrivateDispatchCommitRequest
		if err := readPrivateFrame(stdin, &request); err != nil {
			fmt.Fprintln(stderr, "mc: invalid private commit frame")
			return 1
		}
		if err := validatePrivateReleaseIdentity(request.ReleaseBuildID, request.ControlVersion,
			request.SpineSchemaID, request.ConfigSchemaVersion); err != nil {
			fmt.Fprintln(stderr, "mc:", err)
			return 1
		}
		out, err := withSpine(func(db *sql.DB) (any, error) {
			return verbs.DispatchCommitPrivateDB(db, request)
		})
		if err != nil {
			fmt.Fprintln(stderr, "mc: private commit refused:", err)
			return 1
		}
		if err := writePrivateFrame(stdout, out); err != nil {
			fmt.Fprintln(stderr, "mc: write private commit frame:", err)
			return 2
		}
		return 0
	}
	return 2
}

func validatePrivateReleaseIdentity(build string, control int, schema string, config int) error {
	if !validReleaseBuildID(build) || build != releaseBuildID || control != gatewayControlVersion ||
		schema != substrate.CurrentSchemaID || config != configSchemaVersion {
		return fmt.Errorf("private dispatch release identity mismatch")
	}
	return nil
}

// brokerTickRefusal is one round's broker-side refusal: the code and error
// writeBrokerRefusal would have written for it.
type brokerTickRefusal struct {
	code string
	err  error
}

// brokerPrepareCommit runs ONE TICK over the private crossing: the plan
// frame names the Projects the tick walks; the Collie stage is one prepare
// round with project ""; then
// one round per Project — prepare, and for a candidate attest → commit.
// The wiki loop follows: one wiki round for each Project.
// The tick object {"action":"tick","actions":[...],"idle":[...]} is written
// once. A broker refusal of the Collie stage or of a pipeline round appends
// the refusal envelope to actions and ends the loop of the pipeline rounds.
// The wiki loop still runs, the tick is written, and the exit code is 0 when
// at least one round completed, so the resident effects what was committed.
// A failure before any round completed keeps the single-refusal behaviour.
func brokerPrepareCommit(
	deploymentUUID string,
	collieInventory []verbs.PrivateCollieContainer,
	pipelineLiveness []verbs.PrivatePipelineLiveness,
	stdout, stderr io.Writer,
) int {
	home, err := verbs.ResolveDispatchHome()
	if err != nil {
		return writeBrokerRefusal(stdout, stderr, "preflight.frame_invalid", err)
	}
	// The enabled runtime catalog is host authority, resolved before prepare
	// because the lease-free Collie branch finalizes inside prepare and never
	// reaches attest. An unreadable config resolves the explicit unavailable
	// capability: the pipeline lane still refuses at attest, and the Collie lane
	// simply launches nothing.
	capability := verbs.ResolveRuntimeCapability(home)

	plan, err := verbs.NewPrivateDispatchPlanRequest(releaseBuildID, deploymentUUID, gatewayControlVersion, configSchemaVersion)
	if err != nil {
		return writeBrokerRefusal(stdout, stderr, "preflight.frame_invalid", err)
	}
	var order verbs.PrivateDispatchPlanResponse
	if err := callPrivateHelper("__dispatch-plan", plan, &order); err != nil {
		return writeBrokerRefusal(stdout, stderr, "health.helper_unavailable", err)
	}
	if order.Version != 1 || order.ReleaseBuildID != releaseBuildID || order.ControlVersion != gatewayControlVersion ||
		order.SpineSchemaID != substrate.CurrentSchemaID || order.ConfigSchemaVersion != configSchemaVersion ||
		order.DeploymentUUID != deploymentUUID || order.DispatchRequestID != plan.DispatchRequestID || order.Projects == nil {
		return writeBrokerRefusal(stdout, stderr, "preflight.frame_invalid", fmt.Errorf("private plan response identity mismatch"))
	}

	var tick brokerTick
	rounds := append([]string{""}, order.Projects...)
	for _, project := range rounds {
		effect, refusal := brokerRound(home, deploymentUUID, project, "", "", "", capability, collieInventory, pipelineLiveness)
		ended, first, err := tick.fold(project, effect, refusal)
		if err != nil {
			fmt.Fprintln(stderr, "mc: write tick entry:", err)
			return 2
		}
		if first != nil {
			return writeBrokerRefusal(stdout, stderr, first.code, first.err)
		}
		if ended {
			fmt.Fprintln(stderr, "mc:", tick.refusal.err.Error())
			break
		}
	}
	brokerWikiLoop(&tick, home, deploymentUUID, order.Projects, capability, collieInventory, pipelineLiveness, stderr)
	encoded, err := tick.marshal()
	if err != nil {
		fmt.Fprintln(stderr, "mc: write tick result:", err)
		return 2
	}
	if err := writeOrdinaryResult(stdout, encoded); err != nil {
		fmt.Fprintln(stderr, "mc: write final dispatch result:", err)
		return 2
	}
	return 0
}

// brokerWikiLoop is the wiki loop of the tick. It reads the Project list one
// time for the repository root of each Project. A failed read adds one refusal
// envelope to the tick, and no wiki round runs.
func brokerWikiLoop(
	tick *brokerTick,
	home, deploymentUUID string,
	projects []string,
	capability verbs.PrivateRuntimeCapability,
	collieInventory []verbs.PrivateCollieContainer,
	pipelineLiveness []verbs.PrivatePipelineLiveness,
	stderr io.Writer,
) {
	listed, err := readHostProjectList()
	if err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		if err := tick.foldWiki(nil, &brokerTickRefusal{code: "health.helper_unavailable", err: err}); err != nil {
			fmt.Fprintln(stderr, "mc: write tick entry:", err)
		}
		return
	}
	brokerWikiRounds(tick, home, deploymentUUID, projects, projectRootsOf(listed),
		capability, collieInventory, pipelineLiveness, stderr)
}

// brokerWikiRounds runs one wiki round for each Project of the tick order. It
// reads the accepted commit of each Project at the same time before the first
// round. roots holds the repository root of each Project. A refused wiki round
// ends only that round. A helper call that reached a time limit ends the loop.
func brokerWikiRounds(
	tick *brokerTick,
	home, deploymentUUID string,
	projects []string,
	roots map[string]string,
	capability verbs.PrivateRuntimeCapability,
	collieInventory []verbs.PrivateCollieContainer,
	pipelineLiveness []verbs.PrivatePipelineLiveness,
	stderr io.Writer,
) {
	selected := make(map[string]string, len(projects))
	for _, project := range projects {
		selected[project] = roots[project]
	}
	observations := acceptedCommitsAt(selected)
	for _, project := range projects {
		effect, refusal := brokerWikiRound(home, deploymentUUID, project, selected[project], observations[project],
			capability, collieInventory, pipelineLiveness)
		if refusal != nil {
			fmt.Fprintln(stderr, "mc:", refusal.err.Error())
		}
		if err := tick.foldWiki(effect, refusal); err != nil {
			fmt.Fprintln(stderr, "mc: write tick entry:", err)
			if err := tick.foldWiki(nil, &brokerTickRefusal{code: "preflight.frame_invalid", err: err}); err != nil {
				fmt.Fprintln(stderr, "mc: write tick entry:", err)
			}
		}
		if refusal != nil && errors.Is(refusal.err, errPrivateHelperTimeLimit) {
			return
		}
	}
}

// brokerWikiRound runs the wiki round of one Project. A panic of the round is
// a refusal of that round.
func brokerWikiRound(
	home, deploymentUUID, project, root, observation string,
	capability verbs.PrivateRuntimeCapability,
	collieInventory []verbs.PrivateCollieContainer,
	pipelineLiveness []verbs.PrivatePipelineLiveness,
) (effect json.RawMessage, refusal *brokerTickRefusal) {
	defer func() {
		if recovered := recover(); recovered != nil {
			effect, refusal = nil, &brokerTickRefusal{code: "preflight.frame_invalid",
				err: fmt.Errorf("the wiki round of Project %q stopped: %v", project, recovered)}
		}
	}()
	return brokerRound(home, deploymentUUID, project, verbs.PrivateWikiRound, root, observation,
		capability, collieInventory, pipelineLiveness)
}

// brokerTick is the tick object under assembly: the ordered committed effects,
// the idle entries of the pipeline rounds that committed nothing, and the
// refusal that ended the loop of the pipeline rounds, if any.
type brokerTick struct {
	actions   []json.RawMessage
	idle      []json.RawMessage
	completed int
	refusal   *brokerTickRefusal
}

// fold folds the outcome of the Collie stage or of one pipeline round into the
// tick. A round that the crossing refused, or whose committed effect the broker
// cannot read, ends the loop of the pipeline rounds: before any round completed
// it is returned as first (the caller writes it as the single broker refusal);
// after a completed round it is appended to actions as the error envelope
// {"error":{"code","message"}}, the same element the in-process seam appends,
// and ended is true. The effects of the wiki loop follow that envelope. Every
// other effect is appended to actions, except a pipeline round's idle result,
// which is listed under idle with its reason (the Collie stage's idle result is
// recorded nowhere).
func (t *brokerTick) fold(project string, effect json.RawMessage, refusal *brokerTickRefusal) (ended bool, first *brokerTickRefusal, err error) {
	if refusal == nil {
		action, reason, probeErr := probeEffect(effect)
		if probeErr != nil {
			refusal = &brokerTickRefusal{code: "preflight.frame_invalid", err: probeErr}
		} else {
			t.completed++
			if action != "idle" {
				t.actions = append(t.actions, effect)
				return false, nil, nil
			}
			if project == "" {
				return false, nil, nil
			}
			entry, err := json.Marshal(map[string]any{"project": project, "reason": reason})
			if err != nil {
				return false, nil, err
			}
			t.idle = append(t.idle, entry)
			return false, nil, nil
		}
	}
	if t.completed == 0 {
		return true, refusal, nil
	}
	envelope, err := json.Marshal(map[string]any{
		"error": map[string]any{"code": refusal.code, "message": refusal.err.Error()},
	})
	if err != nil {
		return false, nil, err
	}
	t.refusal = refusal
	t.actions = append(t.actions, envelope)
	return true, nil, nil
}

// foldWiki folds the outcome of one wiki round into the tick. A refusal is
// appended to actions as the error envelope {"error":{"code","message"}}. An
// idle result adds nothing. Each other effect is appended to actions.
func (t *brokerTick) foldWiki(effect json.RawMessage, refusal *brokerTickRefusal) error {
	if refusal != nil {
		envelope, err := json.Marshal(map[string]any{
			"error": map[string]any{"code": refusal.code, "message": refusal.err.Error()},
		})
		if err != nil {
			return err
		}
		t.actions = append(t.actions, envelope)
		return nil
	}
	action, _, err := probeEffect(effect)
	if err != nil {
		return err
	}
	if action != "idle" {
		t.actions = append(t.actions, effect)
	}
	return nil
}

// marshal encodes the tick object {"action":"tick","actions":[...],"idle":[...]};
// both lists are present even when empty.
func (t *brokerTick) marshal() ([]byte, error) {
	actions, idle := t.actions, t.idle
	if actions == nil {
		actions = []json.RawMessage{}
	}
	if idle == nil {
		idle = []json.RawMessage{}
	}
	return json.Marshal(map[string]any{"action": "tick", "actions": actions, "idle": idle})
}

// probeEffect decodes nothing but the closed discriminators of one round's
// committed effect: its action and, for an idle result, its reason.
func probeEffect(effect []byte) (action, reason string, err error) {
	var probe struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if len(effect) == 0 || !json.Valid(effect) {
		return "", "", fmt.Errorf("helper returned an invalid round result")
	}
	if err := json.Unmarshal(effect, &probe); err != nil {
		return "", "", fmt.Errorf("helper returned an invalid round result")
	}
	return probe.Action, probe.Reason, nil
}

// brokerRound runs one round of the tick: prepare for project ("" is the
// Collie stage) and, for a candidate frame, attest and
// commit. It returns the round's committed effect bytes, or the broker
// refusal that ended the round.
//
// round is "" for the Collie stage and a pipeline round. A wiki round carries
// the accepted commit of the Project in place of the Vision check: observation
// in the prepare frame, and a new read of the repository at root in the commit
// frame.
func brokerRound(
	home, deploymentUUID, project, round, root, observation string,
	capability verbs.PrivateRuntimeCapability,
	collieInventory []verbs.PrivateCollieContainer,
	pipelineLiveness []verbs.PrivatePipelineLiveness,
) (json.RawMessage, *brokerTickRefusal) {
	refuse := func(code string, err error) (json.RawMessage, *brokerTickRefusal) {
		return nil, &brokerTickRefusal{code: code, err: err}
	}
	request, err := verbs.NewPrivateDispatchPrepareRequest(
		releaseBuildID, deploymentUUID, gatewayControlVersion, configSchemaVersion,
		project, collieInventory, pipelineLiveness, capability)
	if err != nil {
		return refuse("preflight.frame_invalid", err)
	}
	wiki := round == verbs.PrivateWikiRound
	if wiki {
		request.Round, request.AcceptedCommit = round, observation
	} else {
		request.VisionCheck, err = inspectDispatchVision(project)
		if err != nil {
			return refuse("health.helper_unavailable", err)
		}
	}
	var prepared verbs.PrivateDispatchPrepareResponse
	if err := callPrivateHelper("__dispatch-prepare", request, &prepared); err != nil {
		return refuse("health.helper_unavailable", err)
	}
	if prepared.ReleaseBuildID != releaseBuildID || prepared.ControlVersion != gatewayControlVersion ||
		prepared.SpineSchemaID != substrate.CurrentSchemaID || prepared.ConfigSchemaVersion != configSchemaVersion ||
		prepared.DeploymentUUID != deploymentUUID || prepared.DispatchRequestID != request.DispatchRequestID {
		return refuse("preflight.frame_invalid", fmt.Errorf("private prepare response identity mismatch"))
	}
	if prepared.Kind == "final" {
		if prepared.Candidate != nil || prepared.Final == nil || len(*prepared.Final) == 0 {
			return refuse("preflight.frame_invalid", fmt.Errorf("malformed private final response"))
		}
		// Producer fence: a capability-consuming final names the exact catalog
		// it resolved against, and it must be the one this broker carried in.
		if prepared.RuntimeCapabilityDigest != "" &&
			prepared.RuntimeCapabilityDigest != capability.Digest {
			return refuse("preflight.frame_invalid",
				fmt.Errorf("private final names a runtime catalog this dispatch did not carry"))
		}
		// Converse fence: the helper never gets to decide that a runtime-bearing
		// effect owes no drift check. The effect's own action is the discriminator.
		if capabilityConsumingFinal(*prepared.Final) && prepared.RuntimeCapabilityDigest == "" {
			return refuse("preflight.frame_invalid",
				fmt.Errorf("private final resolved a runtime binding without naming its catalog"))
		}
		if verbs.RuntimeCapabilityDrifted(home, prepared.RuntimeCapabilityDigest) {
			return refuse("preflight.stale",
				fmt.Errorf("runtime configuration changed during prepare; abandoning the resolved effect"))
		}
		return *prepared.Final, nil
	}
	// A candidate carries a spawn that owes attest and commit. The frame must
	// carry exactly the candidate and name this round's Project.
	if prepared.Kind != "candidate" || prepared.Candidate == nil || prepared.Final != nil ||
		prepared.Candidate.Project != project {
		return refuse("preflight.frame_invalid", fmt.Errorf("malformed private candidate response"))
	}
	commit, err := verbs.DispatchAttestPrivate(home, prepared)
	if err != nil {
		return refuse("preflight.frame_invalid", err)
	}
	commit = verbs.DispatchRecheckPrivate(home, prepared, commit)
	if wiki {
		commit.Round, commit.AcceptedCommit = round, acceptedCommitAt(root)
	} else {
		commit.VisionCheck, err = inspectDispatchVision(project)
		if err != nil {
			return refuse("health.helper_unavailable", err)
		}
	}
	var result verbs.PrivateDispatchResult
	if err := callPrivateHelper("__dispatch-commit", commit, &result); err != nil {
		return refuse("health.helper_unavailable", err)
	}
	if result.Version != 1 || result.ReleaseBuildID != releaseBuildID ||
		result.ControlVersion != gatewayControlVersion || result.SpineSchemaID != substrate.CurrentSchemaID ||
		result.ConfigSchemaVersion != configSchemaVersion || result.DeploymentUUID != deploymentUUID ||
		result.DispatchRequestID != request.DispatchRequestID || len(result.Result) == 0 {
		return refuse("preflight.frame_invalid", fmt.Errorf("private commit response identity mismatch"))
	}
	return result.Result, nil
}

// capabilityConsumingFinal reports whether a final effect resolved a runtime
// binding out of the carried catalog and therefore owes the drift fence.
//
// The broker relays a final as opaque bytes everywhere else, and deliberately
// so. This is the one thing it must read, because the alternative is trusting
// the helper's own claim that an effect owes no fence — exactly the fail-open
// shape a producer-only fence has. It decodes nothing but the closed action
// discriminator; an unparseable body is treated as owing the fence, never as
// exempt.
func capabilityConsumingFinal(final []byte) bool {
	var probe struct {
		Action         string          `json:"action"`
		RuntimeBinding json.RawMessage `json:"runtime_binding"`
	}
	if err := json.Unmarshal(final, &probe); err != nil {
		return true
	}
	return probe.Action == "collie-wake" || len(probe.RuntimeBinding) != 0
}

func writeOrdinaryResult(w io.Writer, result []byte) error {
	// The tick object has no size limit of its own. Each effect arrived in one
	// private frame, so the frame limit is the limit of one effect.
	if len(result) == 0 || result[0] != '{' || !json.Valid(result) {
		return fmt.Errorf("helper returned an invalid final result")
	}
	return writeAll(w, append(append([]byte{}, result...), '\n'))
}

// privateHelperExitError preserves the helper's OWN exit code across the
// crossing. The helper already classified its refusal — 1 domain rejection,
// 2 usage or environment — and a broker that flattens every failure
// into one wrapper destroys that verdict on the only path production takes.
// The message is unchanged, so callers that merely print it are unaffected.
type privateHelperExitError struct {
	message string
	code    int
	// refusal is the error envelope that the helper wrote on its standard
	// output, when the output holds exactly one envelope.
	refusal *verbs.DomainError
}

func (e *privateHelperExitError) Error() string { return e.message }

// Unwrap marks the exit of a helper that reached its own time limit.
func (e *privateHelperExitError) Unwrap() error {
	if e.code == privateHelperTimeoutExitCode {
		return errPrivateHelperTimeLimit
	}
	return nil
}

// errPrivateHelperTimeLimit marks a helper call that reached a time limit: the
// wall limit, the limit for no progress, or the time limit of the helper.
var errPrivateHelperTimeLimit = errors.New("private helper time limit")

// privateHelperTimeLimitError is a helper call that the broker stopped at a
// time limit.
type privateHelperTimeLimitError struct{ message string }

func (e *privateHelperTimeLimitError) Error() string { return e.message }

func (e *privateHelperTimeLimitError) Unwrap() error { return errPrivateHelperTimeLimit }

// privateHelperVerbError re-raises a helper failure under the classification
// the helper itself chose. A crossing that never produced an exit code at all
// (timeout, docker missing, oversized frame) carries code 0 and stays an
// environment failure — it is not a domain verdict about the request.
func privateHelperVerbError(context string, err error) error {
	var exit *privateHelperExitError
	if errors.As(err, &exit) && exit.code == 1 {
		return verbs.Domainf("%s: %v", context, err)
	}
	return verbs.Usagef("%s: %v", context, err)
}

// privateHelperRefusal relays a domain rejection of the helper with its own
// code and message, so that the caller can act on the code. Each other failure
// keeps the text of privateHelperVerbError.
func privateHelperRefusal(context string, err error) error {
	var exit *privateHelperExitError
	if errors.As(err, &exit) && exit.code == 1 && exit.refusal != nil {
		return exit.refusal
	}
	return privateHelperVerbError(context, err)
}

// helperRefusal reads the error envelope of a refused helper. The result is
// nil unless the output holds exactly one envelope with a code and a message.
func helperRefusal(output []byte) *verbs.DomainError {
	var envelope struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&envelope); err != nil ||
		envelope.Error == nil || envelope.Error.Code == "" || envelope.Error.Message == "" {
		return nil
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil
	}
	code := envelope.Error.Code
	if code == "domain-rejection" {
		code = ""
	}
	return &verbs.DomainError{Code: code, Msg: envelope.Error.Message}
}

func callPrivateHelper(verb string, request, response any) error {
	var input bytes.Buffer
	if err := writePrivateFrame(&input, request); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), helperWallTimeout)
	defer cancel()
	// Pass an absolute deadline fixed before Docker startup. Even if docker
	// exec spends most of the broker allowance starting the container-side
	// process, that process inherits only the remaining time (or exits 124
	// immediately when the deadline has already passed).
	helperDeadline := time.Now().Add(privateHelperSelfTimeout).UnixMilli()
	helperArgs := operatorHelperArgs([]string{verb, "--deadline-unix-ms", strconv.FormatInt(helperDeadline, 10)})
	cmd := exec.CommandContext(ctx, "docker", append([]string{"exec", "-i", helperContainerName(), "mc"}, helperArgs...)...)
	cmd.Stdin = &input
	var output boundedBuffer
	output.limit = maxPrivateFrameBytes + 4
	output.progress = make(chan struct{}, 1)
	cmd.Stdout = &output
	// The helper's own refusal reason travels on stderr. Discarding it made
	// every broker failure report as the bare string "private helper <verb>
	// failed", which is why the fixture notes that a private-helper refusal is
	// "diagnosable only by reproducing the prepare host-side" — the reason was
	// destroyed here, not missing. It is bounded and only ever appended to the
	// failure path, so the success path stays byte-silent as its test requires.
	var helperErr boundedBuffer
	helperErr.limit = maxHelperStderrBytes
	cmd.Stderr = &helperErr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("private helper %s failed to start", verb)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	noProgress := time.NewTimer(helperNoProgressTimeout)
	defer noProgress.Stop()
	wallLimit := func() error {
		return &privateHelperTimeLimitError{
			message: fmt.Sprintf("private helper %s exceeded wall allowance", verb),
		}
	}
	for {
		select {
		case err := <-waited:
			if err != nil {
				// The wall limit stops the process through the context, so
				// the exit of that process is the wall limit.
				if ctx.Err() != nil {
					return wallLimit()
				}
				failure := &privateHelperExitError{
					message: fmt.Sprintf("private helper %s failed%s", verb, helperReason(&helperErr, err)),
				}
				var exit *exec.ExitError
				if errors.As(err, &exit) {
					failure.code = exit.ExitCode()
				}
				if !output.overflow {
					failure.refusal = helperRefusal(output.Bytes())
				}
				return failure
			}
			goto complete
		case <-output.progress:
			if !noProgress.Stop() {
				select {
				case <-noProgress.C:
				default:
				}
			}
			noProgress.Reset(helperNoProgressTimeout)
		case <-noProgress.C:
			_ = cmd.Process.Kill()
			<-waited
			return &privateHelperTimeLimitError{
				message: fmt.Sprintf("private helper %s made no I/O progress for %s", verb, helperNoProgressTimeout),
			}
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-waited
			return wallLimit()
		}
	}

complete:
	if output.overflow {
		return fmt.Errorf("private helper %s response exceeded 1 MiB", verb)
	}
	return readPrivateFrame(bytes.NewReader(output.Bytes()), response)
}

type boundedBuffer struct {
	bytes.Buffer
	limit    int
	overflow bool
	progress chan struct{}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if len(p) > 0 && b.progress != nil {
		select {
		case b.progress <- struct{}{}:
		default:
		}
	}
	remaining := b.limit - b.Len()
	if remaining <= 0 {
		b.overflow = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.overflow = true
		_, _ = b.Buffer.Write(p[:remaining])
		return len(p), nil
	}
	return b.Buffer.Write(p)
}

func writePrivateFrame(w io.Writer, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(body) == 0 || len(body) > maxPrivateFrameBytes {
		return fmt.Errorf("private frame length %d is outside 1..1048576", len(body))
	}
	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(body)))
	return writeAll(w, append(prefix[:], body...))
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func readPrivateFrame(r io.Reader, value any) error {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(prefix[:])
	if n == 0 || n > maxPrivateFrameBytes {
		return fmt.Errorf("private frame length %d is outside 1..1048576", n)
	}
	body := make([]byte, int(n))
	if _, err := io.ReadFull(r, body); err != nil {
		return err
	}
	var trailing [1]byte
	if n, err := r.Read(trailing[:]); n != 0 || (err != nil && err != io.EOF) {
		return fmt.Errorf("private frame has trailing bytes")
	}
	if err := json.Unmarshal(body, value); err != nil {
		return err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if !bytes.Equal(body, canonical) {
		return fmt.Errorf("private frame is not the closed canonical encoding")
	}
	return nil
}

// helperReason renders the helper's own failure evidence for the broker error.
// Without it every private-helper failure reads as the bare "private helper
// <verb> failed" while the reason sits unread on the child's stderr.
//
// The wait error is included because it carries the case the stderr cannot:
// exit 124 is the container-side absolute deadline expiring, which is the
// signature of a `docker exec` that spent the broker allowance starting up.
func helperReason(stderr *boundedBuffer, wait error) string {
	var parts []string
	if wait != nil {
		parts = append(parts, wait.Error())
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		msg = strings.ReplaceAll(msg, "\n", "; ")
		if stderr.overflow {
			msg += " …(truncated)"
		}
		parts = append(parts, msg)
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ": ") + ")"
}
