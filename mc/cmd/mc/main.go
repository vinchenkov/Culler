// Command mc is the Culler binary — the sole spine writer.
// The verb surface is pinned: single JSON object on stdout, diagnostics on
// stderr, exit 0 success / 1 domain rejection / 2 usage or environment error.
//
// Spine reachability: MC_SPINE names the directly reachable spine file; when
// MC_HELPER=<container> is set and MC_SPINE is not, mc self-delegates — it
// re-invokes itself as `docker exec -i <helper> mc <argv…>`, passing
// stdin/stdout/stderr and the exit code through untouched.
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"mc/domain"
	"mc/mclog"
	"mc/substrate"
	"mc/verbs"
)

func main() {
	if filepath.Base(os.Args[0]) == "culler" {
		os.Exit(runCuller(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is the whole invocation: it opens this process's log, dispatches, and
// records one cli.invoked line with the outcome. The dispatch itself is
// dispatchArgs, unchanged — the wrapper exists so that every return path out of
// it is counted, including the ones that delegate into the helper and the ones
// that fail argument parsing before any verb runs.
//
// Closing the logger matters: rotation reopens the fd, and a process that exits
// without closing can leave the last record unflushed on a full disk.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if handled, code := runOperatorHelp(args, stdout, stderr); handled {
		return code
	}
	defer enterOperatorOwner(args)()
	// Saved and restored, not simply cleared: `mc __onboard verify` re-enters
	// run() for each smoke command, and a nested call that reset cliLog to nil
	// on the way out would silently stop the OUTER invocation from logging
	// anything for the rest of its life.
	previous := cliLog
	started := time.Now()
	cliLog = openCLILogger()
	exit := dispatchArgs(args, stdin, stdout, stderr)
	if len(args) > 0 {
		logInvocation(args, exit, started)
	}
	if cliLog != nil {
		_ = cliLog.Close()
	}
	cliLog = previous
	return exit
}

func dispatchArgs(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if handled, code := operatorPreDispatch(args, stdin, stdout, stderr); handled {
		return code
	}
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: mc <verb> …")
		writeErrorEnvelope(stdout, stderr, "usage", "usage: mc <verb> …")
		return 2
	}
	if args[0] == "__project-docker" {
		return runProjectDocker(args[1:], stdin, stdout, stderr)
	}
	if canInspectProjectVision() && len(args) >= 2 && args[0] == "project" && args[1] == "delete" {
		return brokerProjectDelete(args, stdout, stderr)
	}
	if args[0] == "__connection-owner" {
		return runConnectionOwner(args, stdin, stdout, stderr)
	}
	if args[0] == "__dashboard-upload" {
		result, err := commandDashboardUpload(args[1:], stdin)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := writeJSON(stdout, result); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		return 0
	}
	if args[0] == "wiki" {
		return runWikiRead(args[1:], stdout, stderr)
	}
	if args[0] == "__wiki-export" {
		return runWikiExport(args[1:], stdout, stderr)
	}
	if args[0] == "__agentsview-access" {
		return runAgentsViewAccess(args[1:], stdout, stderr)
	}
	if args[0] == "__project-home" {
		return runProjectHome(args[1:], stdout, stderr)
	}
	if args[0] == "__setup" {
		return runTerminalSetup(args[1:], stdin, stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "__onboard" && args[1] == "agentsview" {
		return runOnboardAgentsView(args[2:], stdout, stderr)
	}
	if args[0] == "__release" {
		return runPrivateRelease(args[1:], stdout, stderr)
	}
	if args[0] == "__update-reconcile" {
		return runPrivateUpdateReconcile(args, stdout, stderr)
	}
	if args[0] == "__release-database" {
		return runPrivateReleaseDatabase(args, stdin, stdout, stderr)
	}
	if args[0] == "__database-schema" {
		return runPrivateDatabaseSchema(args, stdout, stderr)
	}
	if args[0] == "__redeploy-check" {
		return runRedeployCheck(args, stdout, stderr)
	}
	if args[0] == "__dispatch-plan" || args[0] == "__dispatch-prepare" || args[0] == "__dispatch-commit" {
		return runPrivateDispatch(args, stdin, stdout, stderr)
	}
	if args[0] == "__collie-start" {
		return runPrivateCollieStart(args, stdin, stdout, stderr)
	}
	if args[0] == "__attachment-health-guard" {
		return runAttachmentHealthGuard(args, stdin, stdout, stderr)
	}
	if args[0] == "__attachment-attempt-abort-spine" ||
		args[0] == "__attachment-attempts-expire-due-spine" ||
		args[0] == "__attachment-ingress-reserve-spine" ||
		args[0] == "__attachment-publication-renew-spine" {
		return runPrivateAttachmentSpine(args, stdin, stdout, stderr)
	}
	if args[0] == "__attachment-publisher-spine-rpc" ||
		args[0] == "__attachment-terminal-spine-rpc" {
		return runPrivateAttachmentSpineRPC(args, stdin, stdout, stderr)
	}
	if args[0] == "__attachment-bytes-spine" {
		return runPrivateAttachmentBytesSpine(args, stdin, stdout, stderr)
	}
	if args[0] == "__onboard-spine" {
		return runPrivateOnboardSpine(args, stdin, stdout, stderr)
	}
	if args[0] == "__doctor-runtime" {
		return runPrivateDoctorRuntime(args, stdin, stdout, stderr)
	}
	if args[0] == "__acceptance-snapshot" {
		return runPrivateAcceptanceSnapshot(args, stdout, stderr)
	}
	if args[0] == "__attachment-canary" || args[0] == "__attachment-canary-publish" {
		return runLocal(args, stdin, stdout, stderr)
	}
	if args[0] == "__onboard-state" {
		return runPrivateOnboardState(args, stdin, stdout, stderr)
	}
	if args[0] == "__backup-spine" {
		return runPrivateSpineBackup(args, stdin, stdout, stderr)
	}
	if args[0] == "__restore-spine" {
		return runPrivateSpineRestore(args, stdin, stdout, stderr)
	}
	// Production Home is a composed crossing: host-side canonical-path and
	// mirror operations stay on Darwin, while only the path-free spine frame
	// enters the exact helper. Preflight is host-only and writes nothing.
	//
	// `__onboard` is the front doors' spelling: the whole wizard, the
	// install-time sections, release application, supervision, and the smoke.
	// The agent-facing `onboard` below it carries only the setup sections.
	if shouldDelegateToHelper() && args[0] == "__onboard" {
		if len(args) == 1 || strings.HasPrefix(args[1], "-") {
			return brokerWholeOnboard(args, stdin, stdout, stderr)
		}
		if len(args) >= 2 {
			if _, err := parseOnboardArgs(args[1:]); err != nil {
				return writeVerbError(stdout, stderr, err)
			}
			switch args[1] {
			case "preflight":
				return runLocal(args, stdin, stdout, stderr)
			case "home":
				return brokerOnboardHome(args, true, stdout, stderr)
			case "runtime-auth":
				return brokerOnboardRuntimeAuth(args, stdin, stdout, stderr)
			case "routing", "project", "tunables", "surfaces", "supervision":
				return brokerOnboardState(args, stdout, stderr)
			case "container":
				return brokerOnboardContainer(args, stdout, stderr)
			case "verify":
				return brokerOnboardVerify(args, stdout, stderr)
			}
		}
	}
	if shouldDelegateToHelper() && args[0] == "onboard" {
		a, err := parseOnboardArgs(args[1:])
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := verbs.GateSetupOnboardArgs(a); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		switch a.Section {
		case "home":
			return brokerOnboardHome(args, false, stdout, stderr)
		case "runtime-auth":
			return brokerOnboardRuntimeAuth(args, stdin, stdout, stderr)
		default: // routing | project | tunables | surfaces — gate-verified
			return brokerOnboardState(args, stdout, stderr)
		}
	}
	if len(args) >= 2 && args[0] == "project" && isProjectSetupAction(args[1]) {
		result, err := commandProjectSetup(args[1:], stdin)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err := writeJSON(stdout, result); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		return 0
	}
	if shouldDelegateToHelper() && len(args) >= 2 && args[0] == "environment" && args[1] == "work" {
		return brokerEnvironmentWork(args, stdin, stdout, stderr)
	}
	if len(args) >= 2 && args[0] == "environment" && args[1] == "image-check" {
		result, err := commandEnvironmentImageCheck(stdin)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		if err = writeJSON(stdout, result); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		return 0
	}
	if canInspectProjectVision() && len(args) >= 2 && args[0] == "environment" && args[1] == "retry" {
		return brokerEnvironmentRetry(args, stdin, stdout, stderr)
	}
	if canInspectProjectVision() && len(args) >= 2 && args[0] == "environment" && args[1] == "save" {
		return brokerEnvironmentSave(args, stdin, stdout, stderr)
	}
	if canInspectProjectVision() && len(args) == 2 && args[0] == "project" && args[1] == "list" {
		return brokerProjectList(stdout, stderr)
	}
	if canInspectProjectVision() && len(args) >= 2 && args[0] == "delivery" && args[1] == "start" {
		return brokerDeliveryStart(args, stdin, stdout, stderr)
	}
	if shouldDelegateToHelper() && args[0] == "doctor" {
		return brokerDoctor(args, stdout, stderr)
	}
	if shouldDelegateToHelper() && args[0] == "backup" {
		return brokerBackup(args, stdout, stderr)
	}
	if shouldDelegateToHelper() && args[0] == "__reset" {
		return brokerReset(args, stdout, stderr)
	}
	// Uninstall is the one host verb that must NOT self-delegate: the helper is
	// among the things it destroys, so a delegated run would remove the
	// container it is executing inside. It runs host-side and crosses into the
	// lock domain only for the workspace-root read.
	if args[0] == "__uninstall" {
		return brokerUninstall(args, stdout, stderr)
	}
	if shouldDelegateToHelper() && args[0] == "__acceptance" {
		return brokerAcceptance(args, stdout, stderr)
	}
	if shouldDelegateToHelper() && len(args) > 1 && args[0] == "collie" && args[1] == "start" {
		return brokerCollieStart(args, stdout, stderr)
	}
	if shouldDelegateToHelper() && (args[0] == "__attachment-attempt-abort" ||
		args[0] == "__attachment-attempts-expire-due" ||
		args[0] == "__attachment-terminals-reconcile" ||
		args[0] == "__attachment-ingress-reserve" ||
		args[0] == "__attachment-publication-renew" ||
		args[0] == "__attachment-publication-publish") {
		return brokerPrivateAttachment(args, stdin, stdout, stderr)
	}
	// The attachment byte read is a composed crossing that must START here:
	// its file half verifies $MC_HOME/attachments host-side, and its spine
	// half crosses into the helper on its own when this process cannot open
	// the spine directly.
	if args[0] == "__attachment-bytes" {
		return runAttachmentBytesRead(args[1:], stdout, stderr)
	}
	// The launch-time identity recheck reads HOST files by definition: it must
	// run locally even when every ordinary verb self-delegates into the helper.
	if args[0] == "__prune-artifacts" || args[0] == "__repository" || args[0] == "__artifact-host-path" || args[0] == "__artifact-roots" || args[0] == "__task-artifacts" || args[0] == "__repository-file" || args[0] == "__attachment-tree-prepare" || args[0] == "__mount-recheck" || args[0] == "__setup-seeding-projection" {
		return runLocal(args, stdin, stdout, stderr)
	}

	// The dashboard's file-read verbs read HOST files only — config.toml,
	// routing.md, $MC_HOME/logs, $MC_HOME/health — and never open the spine.
	// Delegated into the helper they resolve MC_HOME container-side and
	// answer for a home that does not exist there.
	//
	// `health` belongs in this list for a sharper reason than the others: its
	// whole purpose is to answer while the spine is unreachable and Docker is
	// down. Routing it through the helper would make it need the very things it
	// exists to report on.
	if len(args) > 1 && ((args[0] == "run" || args[0] == "collie") && args[1] == "launch-check") {
		return runLocal(args, stdin, stdout, stderr)
	}
	if args[0] == "discord" || args[0] == "__discord-config" || args[0] == "evaluation" || args[0] == "system" || args[0] == "connection" || args[0] == "agent-profile" || args[0] == "config" || args[0] == "routing" || args[0] == "logs" || args[0] == "health" {
		return runLocal(args, stdin, stdout, stderr)
	}

	// Self-delegation: host-side mc with no direct spine path but a named
	// warm helper delegates the whole invocation into the lock domain.
	if shouldDelegateToHelper() {
		if _, err := ensureRuntimeHelper(); err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("ensure warm helper: %v", err))
		}
		if args[0] == "dispatch" {
			return brokerDispatch(args, stdin, stdout, stderr)
		}
		code := delegate(args, stdin, stdout, stderr)
		// The helper registered the Project; the skill store it gets under
		// the host's MC_HOME is the host's to create.
		if code == 0 && args[0] == "project" && len(args) > 2 && args[1] == "add" {
			extendSkillStoreOnHost(args[2], stderr)
		}
		return code
	}

	return runLocal(args, stdin, stdout, stderr)
}

// delegatedStderrKeep bounds what a delegated refusal can put in the host's
// log. A verb that floods stderr must not be able to flood the log through it;
// the record's msg is capped again at 2 KiB by the encoder, so this is the
// memory bound, not the record bound.
const delegatedStderrKeep = 8 << 10

// boundedTee keeps the FIRST bytes written to it and silently drops the rest.
// First, not last: a refusal says what it refused on its opening line, and a
// ring buffer would keep the stack trace and lose the sentence.
type boundedTee struct {
	buf   []byte
	limit int
}

func (t *boundedTee) Write(p []byte) (int, error) {
	if room := t.limit - len(t.buf); room > 0 {
		if len(p) < room {
			room = len(p)
		}
		t.buf = append(t.buf, p[:room]...)
	}
	return len(p), nil
}

// text is the trimmed capture, with the "mc: " prefix the inner process added
// removed — the prefix names the writer, and the writer is already `comp`.
func (t *boundedTee) text() string {
	return strings.TrimPrefix(strings.TrimSpace(string(t.buf)), "mc: ")
}

// envelopeCode reads the refusal code out of a captured error envelope. A
// stdout that is not one — a truncated capture, or a verb that failed before
// writing anything — yields "delegated", which says exactly what is known: the
// inner process refused and did not say why in a form this one can read.
func envelopeCode(captured []byte) string {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(captured), &envelope); err != nil || envelope.Error.Code == "" {
		return "delegated"
	}
	return envelope.Error.Code
}

// hasFlag reports whether argv already carries a flag, in either the `-name` or
// `--name` spelling and with either `=value` or a separate value.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		trimmed := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		if trimmed == name || strings.HasPrefix(trimmed, name+"=") {
			return true
		}
	}
	return false
}

func runLocal(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	out, err := dispatchVerb(args, stdin)
	if err != nil {
		return writeVerbError(stdout, stderr, annotateOperatorUsage(args, err))
	}
	if err := writeJSON(stdout, out); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}

// writeVerbError is the one place a verb failure becomes the exit code plus
// error envelope: 1 domain rejection, 2 usage or environment.
func writeVerbError(stdout, stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "mc:", err)
	exitCode := 1
	errorCode := "domain-rejection"
	var usage *verbs.UsageError
	if errors.As(err, &usage) {
		exitCode = 2
		errorCode = "usage"
	} else {
		var domainErr *verbs.DomainError
		if errors.As(err, &domainErr) && domainErr.Code != "" {
			errorCode = domainErr.Code
		}
	}
	// The structured twin of the line above. The stderr sentence is the CLI
	// contract and does not change; this is the copy that survives the caller
	// throwing stderr away, which is what a resident tick does with all of it.
	//
	// The level is a literal, not a function of errorCode. A refusal carries its
	// own code as a field and the reader can filter on it. A warn/error level
	// derived from the code would be an interpretation, and the log carries
	// no interpretation.
	cliLog.Warn("cli.verb_refused", err.Error(), mclog.Ctx{
		"code": errorCode,
		"exit": exitCode,
	})
	writeErrorEnvelope(stdout, stderr, errorCode, err.Error())
	return exitCode
}

func delegate(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	helper := helperContainerName()
	argv := append([]string{"exec", "-i", helper, "mc"}, operatorHelperArgs(args)...)
	cmd := exec.Command("docker", argv...)
	cmd.Stdin = stdin
	// Both streams are teed for the same reason, and neither is altered: the
	// caller receives every byte the inner mc wrote. stdout is captured because
	// the error ENVELOPE carries the refusal's real code, and a host-side record
	// that could only say "exit 1" would be strictly worse than the one an
	// undelegated verb writes.
	outTee := &boundedTee{limit: delegatedStderrKeep}
	cmd.Stdout = io.MultiWriter(stdout, outTee)
	// Teed, not redirected: the caller still receives every byte the inner mc
	// wrote, unchanged. The copy exists because on darwin EVERY ordinary verb
	// comes through here, and the inner mc runs where $MC_HOME/logs is not
	// visible — so its writeVerbError has a nil logger and the refusal text
	// exists nowhere on the host but the caller's stderr, which the resident
	// throws away. Without this, a delegated refusal is a bare non-zero exit.
	tee := &boundedTee{limit: delegatedStderrKeep}
	cmd.Stderr = io.MultiWriter(stderr, tee)
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			cliLog.Warn("cli.verb_refused", tee.text(), mclog.Ctx{
				"code":  envelopeCode(outTee.buf),
				"exit":  exit.ExitCode(),
				"verb":  args[0],
				"where": "helper",
			})
			return exit.ExitCode()
		}
		// Wrapper-only failure: the inner mc never ran, so there are no
		// delegated bytes to preserve — emit the local envelope.
		fmt.Fprintln(stderr, "mc: self-delegation failed:", err)
		writeErrorEnvelope(stdout, stderr, "usage", "self-delegation failed: "+err.Error())
		return 2
	}
	return 0
}

func writeErrorEnvelope(stdout, stderr io.Writer, code, message string) {
	envelope := map[string]any{
		"error": map[string]any{"code": code, "message": message},
	}
	if err := writeJSON(stdout, envelope); err != nil {
		fmt.Fprintln(stderr, "mc: write error JSON:", err)
	}
}

func writeJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// dispatchVerb parses the verb path and flags, opens the spine, and calls
// the verbs package. Every verb is a thin CLI layer over a testable
// function.
func dispatchVerb(args []string, stdin io.Reader) (any, error) {
	verb := args[0]
	rest := args[1:]
	switch verb {
	case "__attachment-attempt-abort":
		if len(rest) != 0 || !directPrivateAttachmentOK() {
			return nil, verbs.Domainf("private attachment attempt abort requires its resident broker")
		}
		var request privateAttachmentAttemptAbortRequest
		if err := readPrivateFrame(stdin, &request); err != nil || !request.valid() {
			return nil, verbs.Usagef("invalid private attachment abort frame")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __attachment-attempt-abort"); err != nil {
			return nil, err
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return nil, err
		}
		defer lease.Close()
		return withSpine(func(db *sql.DB) (any, error) {
			receipt, err := verbs.AttachmentAttemptAbort(db, nil, request.args())
			if err != nil {
				return nil, err
			}
			return privateAttachmentAttemptTerminalFrame(receipt), nil
		})
	case "__attachment-attempts-expire-due":
		if len(rest) != 0 || !directPrivateAttachmentOK() {
			return nil, verbs.Domainf("private attachment hard-expiry sweep requires its resident broker")
		}
		var request privateAttachmentAttemptsExpireDueRequest
		if err := readPrivateFrame(stdin, &request); err != nil || !request.valid() {
			return nil, verbs.Usagef("invalid private attachment expiry frame")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __attachment-attempts-expire-due"); err != nil {
			return nil, err
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return nil, err
		}
		defer lease.Close()
		return withSpine(func(db *sql.DB) (any, error) {
			page, err := verbs.AttachmentAttemptsExpireDue(
				db, nil, request.After, request.Limit)
			if err != nil {
				return nil, err
			}
			return privateAttachmentExpiryPageFrame(page), nil
		})
	case "__attachment-terminals-reconcile":
		if len(rest) != 0 || !directPrivateAttachmentOK() {
			return nil, verbs.Domainf("private attachment reconciliation requires its resident broker")
		}
		var request privateAttachmentTerminalsReconcileRequest
		if err := readPrivateFrame(stdin, &request); err != nil || !request.valid() {
			return nil, verbs.Usagef("invalid private attachment reconciliation frame")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __attachment-terminals-reconcile"); err != nil {
			return nil, err
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return nil, err
		}
		defer lease.Close()
		home, err := configuredCanonicalHome()
		if err != nil {
			return nil, verbs.Usagef("resolve attachment MC_HOME: %v", err)
		}
		after, _ := request.after()
		return withSpine(func(db *sql.DB) (any, error) {
			page, err := verbs.AttachmentTerminalsReconcile(
				db, nil, home, after, request.Limit)
			if err != nil {
				return nil, err
			}
			return privateAttachmentTerminalPageFrame(page), nil
		})
	case "__attachment-canary":
		if len(rest) != 0 {
			return nil, verbs.Usagef("usage: mc __attachment-canary")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __attachment-canary"); err != nil {
			return nil, err
		}
		home, err := configuredCanonicalHome()
		if err != nil {
			return nil, verbs.Usagef("resolve attachment canary MC_HOME: %v", err)
		}
		return runAttachmentCanary(home)
	case "__attachment-canary-publish":
		if len(rest) != 0 {
			return nil, verbs.Usagef("usage: mc __attachment-canary-publish")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return verbs.AttachmentCanaryPublish(id)
	case "__attachment-tree-prepare":
		if len(rest) != 1 {
			return nil, verbs.Usagef("usage: mc __attachment-tree-prepare <session>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		home, err := configuredCanonicalHome()
		if err != nil {
			return nil, verbs.Usagef("resolve attachment MC_HOME: %v", err)
		}
		return verbs.PrepareAttachmentTree(id, home, rest[0])
	case "__attachment-ingress-reserve":
		if len(rest) != 0 || !directPrivateAttachmentOK() {
			return nil, verbs.Domainf("private attachment ingress reservation requires its resident broker")
		}
		var request privateAttachmentIngressReserveRequest
		if err := readPrivateFrame(stdin, &request); err != nil ||
			!privateAttachmentSession(request.Session) ||
			!privateAttachmentGeneration(request.OwnerGeneration) {
			return nil, verbs.Usagef("invalid private attachment reservation frame")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __attachment-ingress-reserve"); err != nil {
			return nil, err
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return nil, err
		}
		defer lease.Close()
		return withSpine(func(db *sql.DB) (any, error) {
			reservation, err := verbs.AttachmentIngressReserve(db, nil, verbs.AttachmentIngressReserveArgs{
				Session: request.Session, OwnerGeneration: request.OwnerGeneration,
			})
			if err != nil {
				return nil, err
			}
			return privateAttachmentReservationFrame(reservation), nil
		})
	case "__attachment-publication-renew":
		if len(rest) != 0 || !directPrivateAttachmentOK() {
			return nil, verbs.Domainf("private attachment publication renewal requires its resident broker")
		}
		var request privateAttachmentPublicationRenewRequest
		if err := readPrivateFrame(stdin, &request); err != nil || !request.valid() {
			return nil, verbs.Usagef("invalid private attachment renewal frame")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __attachment-publication-renew"); err != nil {
			return nil, err
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return nil, err
		}
		defer lease.Close()
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.AttachmentPublicationRenew(db, nil, request.args())
		})
	case "__attachment-publication-publish":
		if len(rest) != 0 || !directPrivateAttachmentOK() {
			return nil, verbs.Domainf("private attachment publication requires its resident broker")
		}
		var request privateAttachmentPublicationPublishRequest
		if err := readPrivateMessage(stdin, &request); err != nil || !request.valid() {
			return nil, verbs.Usagef("invalid private attachment publication frame")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __attachment-publication-publish"); err != nil {
			return nil, err
		}
		lease, err := acquireCurrentAttachmentCanaryGeneration(request.OwnerGeneration)
		if err != nil {
			return nil, err
		}
		defer lease.Close()
		home, err := configuredCanonicalHome()
		if err != nil {
			return nil, verbs.Usagef("resolve attachment MC_HOME: %v", err)
		}
		stream := &privateAttachmentByteStream{source: stdin}
		result, err := withSpine(func(db *sql.DB) (any, error) {
			return verbs.AttachmentPublicationPublish(db, id, home, request.args(), stream)
		})
		if err != nil {
			return nil, err
		}
		response := result.(verbs.AttachmentPublicationReceipt)
		if response.Size != request.Size {
			return nil, verbs.Domainf(
				"attachment publication receipt size disagrees with its reservation")
		}
		if err := stream.drainExact(request.Size); err != nil {
			return nil, err
		}
		return response, nil
	case "evaluation":
		return commandEvaluation(rest, stdin)
	case "__evaluation-record":
		return commandEvaluationRecord(rest, stdin)
	case "__prune-artifacts":
		if len(rest) != 0 {
			return nil, verbs.Usagef("artifact expiry accepts no arguments")
		}
		return pruneTaskArtifacts()
	case "__artifact-expiry":
		return artifactExpiryCommand(rest)
	case "__artifact-host-path":
		// Host-local by necessity, not by preference: the answer is a HOST
		// path, and the helper this verb would otherwise delegate into mounts
		// the spine volume alone and cannot see the artifact plane at all.
		if len(rest) != 1 {
			return nil, verbs.Usagef("usage: mc __artifact-host-path <container-path>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __artifact-host-path"); err != nil {
			return nil, err
		}
		home, err := configuredCanonicalHome()
		if err != nil {
			return nil, verbs.Usagef("%v", err)
		}
		hostPath, err := verbs.ResolveArtifactHostPath(home, rest[0])
		if err != nil {
			return nil, err
		}
		return map[string]any{"container_path": rest[0], "host_path": hostPath}, nil
	case "__artifact-roots":
		// The map behind a packet's own citations: the render is the role's
		// prose and must not be rewritten in place, so the surface that draws
		// it translates as it draws.
		if len(rest) != 0 {
			return nil, verbs.Usagef("usage: mc __artifact-roots")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __artifact-roots"); err != nil {
			return nil, err
		}
		home, err := configuredCanonicalHome()
		if err != nil {
			return nil, verbs.Usagef("%v", err)
		}
		roots, err := verbs.ArtifactHostRoots(home)
		if err != nil {
			return nil, err
		}
		return map[string]any{"roots": roots}, nil
	case "__task-artifacts":
		return taskArtifactsCommand(rest)
	case "__repository-file":
		return repositoryFileCommand(rest)
	case "__mount-recheck":
		if len(rest) != 1 {
			return nil, verbs.Usagef("usage: mc __mount-recheck <plan-file>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __mount-recheck"); err != nil {
			return nil, err
		}
		return verbs.MountRecheck(rest[0])
	case "__setup-seeding-projection":
		if len(rest) != 1 {
			return nil, verbs.Usagef("usage: mc __setup-seeding-projection <envelope-file>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __setup-seeding-projection"); err != nil {
			return nil, err
		}
		env, err := verbs.ReadSeedingProjectionEnvelope(rest[0])
		if err != nil {
			return nil, err
		}
		commit, err := verbs.RunSeedingProjectionSetup(env)
		return map[string]any{"commit_sha": commit}, err
	case "onboard":
		return cmdOnboard(rest)
	case "__onboard":
		return cmdOnboardFull(rest)
	case "__init":
		return cmdInit(rest)
	case "task":
		return cmdTask(rest)
	case "board":
		return cmdBoard(rest, stdin)
	case "delivery":
		return cmdDelivery(rest, stdin)
	case "__repository":
		if len(rest) != 1 {
			return nil, verbs.Usagef("usage: mc __repository <envelope-file>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __repository"); err != nil {
			return nil, err
		}
		// A stop request of the resident ends the Git child processes of the operation.
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
		defer stop()
		return verbs.ExecuteRepository(ctx, rest[0], func() (map[string]string, error) {
			home, err := configuredCanonicalHome()
			if err != nil {
				return nil, verbs.Usagef("%v", err)
			}
			return verbs.ArtifactHostRoots(home)
		})
	case "pause", "resume":
		if args[0] == "pause" && len(rest) == 1 && rest[0] == "status" {
			return withSpineRead(verbs.PauseStatus)
		}
		if len(rest) != 0 {
			return nil, verbs.Usagef("usage: mc pause [status] | mc resume")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.SetEveryProjectPause(db, id, args[0] == "pause")
		})
	case "system":
		return commandSystem(rest, stdin)
	case "connection":
		return commandConnection(rest, stdin)
	case "__system-state":
		if len(rest) != 1 {
			return nil, verbs.Usagef("The system state action is required.")
		}
		return commandSystemSpine(rest[0], stdin)
	case "project":
		if len(rest) > 0 && isVisionDraftAction(rest[0]) {
			return commandVisionDraft(rest, stdin)
		}
		return cmdProject(rest, stdin)
	case "environment":
		return commandEnvironment(rest, stdin)
	case "packet":
		return cmdPacket(rest)
	case "activity":
		return cmdActivity(rest)
	case "dispatch":
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc dispatch"); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			// The wiki rounds of the tick read the accepted commit of each
			// Project from its repository on this host.
			roots, err := loadProjectRoots(db)
			if err != nil {
				return nil, err
			}
			return verbs.DispatchWithObserver(db, releaseBuildID, gatewayControlVersion,
				substrate.CurrentSchemaID, configSchemaVersion, hostAcceptedCommits{roots: roots})
		})
	case "complete":
		return cmdComplete(rest)
	case "arbiter":
		return cmdArbiter(rest, stdin)
	case "strategist":
		return cmdStrategist(rest, stdin)
	case "attachment":
		return cmdAttachment(rest)
	case "collie":
		return cmdCollie(rest)
	case "outbox":
		return cmdOutbox(rest)
	case "verifier":
		return cmdVerifier(rest)
	case "heartbeat":
		if len(rest) != 1 {
			return nil, verbs.Usagef("usage: mc heartbeat <run_id>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.Heartbeat(db, id, rest[0]) })
	case "run":
		return cmdRun(rest)
	case "doctor":
		if len(rest) != 0 {
			return nil, verbs.Usagef("usage: mc doctor")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		// Doctor opens (or reports on) the spine itself: it must diagnose a
		// missing spine rather than fail on it, and must never create bytes.
		started := time.Now().UTC().Format(time.RFC3339Nano)
		report, err := verbs.Doctor(id, helperSpinePath())
		if err != nil || id != nil {
			return report, err
		}
		return composeSystemDoctor(report, started), nil
	case "backup":
		if len(rest) != 0 {
			return nil, verbs.Usagef("usage: mc backup")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return verbs.Backup(id, helperSpinePath())
	case "__reset":
		fs := newFlags("mc __reset")
		confirm := fs.Bool("confirm", false, "confirm the destructive re-initialization")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return verbs.Reset(id, helperSpinePath(), *confirm)
	case "__trace-register-setup":
		if len(rest) != 3 {
			return nil, verbs.Usagef("usage: mc __trace-register-setup <operation> <native-ref> <file>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __trace-register-setup"); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.RegisterSetupSession(db, nil, rest[0], rest[1], rest[2]) })
	case "__trace-sessions":
		if len(rest) != 0 {
			return nil, verbs.Usagef("usage: mc __trace-sessions")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __trace-sessions"); err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.TraceSessions(db) })
	case "__uninstall-workspace-roots":
		// Private read for `mc __uninstall`, executed inside the lock domain: the
		// Project paths exist only in the spine, and uninstall needs them
		// before it destroys the volume that holds them.
		if len(rest) != 0 {
			return nil, verbs.Usagef("usage: mc __uninstall-workspace-roots")
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.UninstallWorkspaceRoots(db) })
	case "__project-image-digests":
		// The published update reads the images that Projects select. Those
		// images exist only in the spine, so the helper container runs this verb.
		if len(rest) != 0 {
			return nil, verbs.Usagef("usage: mc __project-image-digests")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc __project-image-digests"); err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.ProjectImageDigests(db) })
	case "lease":
		// Pure read: one row per Project — the e2e's lease-state assertion
		// channel, which cannot open the spine volume, and the dashboard's
		// Board.
		if len(rest) != 1 || rest[0] != "list" {
			return nil, verbs.Usagef("usage: mc lease list")
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.LeaseList(db) })
	case "wiki-state":
		// Pure read: one row per Project that is not archived. The resident
		// and the dashboard read the wiki state through it.
		if len(rest) != 1 || rest[0] != "list" {
			return nil, verbs.Usagef("usage: mc wiki-state list")
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.WikiStateList(db) })
	case "settings":
		// Pure read: the one deployment-wide settings row.
		if len(rest) != 1 || rest[0] != "get" {
			return nil, verbs.Usagef("usage: mc settings get")
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.SettingsGet(db) })
	case "config":
		return commandConfig(rest, stdin)
	case "discord":
		return commandDiscord(rest, stdin)
	case "__discord-config":
		if len(rest) != 0 {
			return nil, verbs.Usagef("The Discord runtime request takes no arguments.")
		}
		return commandDiscordRuntime()
	case "__discord-state":
		return commandDiscordState(rest, stdin)
	case "__config-settings":
		return commandConfigSettings(rest, stdin)
	case "agent-profile":
		return commandAgentProfile(rest, stdin)
	case "routing":
		// Host-side only: the role-to-runtime table, collie included. `show`
		// reads it; `set` replaces one role's binding under the same
		// validation as onboarding.
		usage := "usage: mc routing show | mc routing set --role <role> --binding <binding> --expected <binding>"
		if len(rest) == 0 {
			return nil, verbs.Usagef("%s", usage)
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc routing "+rest[0]); err != nil {
			return nil, err
		}
		switch rest[0] {
		case "show":
			if len(rest) != 1 {
				return nil, verbs.Usagef("%s", usage)
			}
			return verbs.RoutingShow()
		case "set":
			fs := newFlags("mc routing set")
			role := fs.String("role", "", "the role to route")
			binding := fs.String("binding", "", "the enabled binding the role uses")
			expected := fs.String("expected", "", "the previous binding of the role")
			if err := parse(fs, rest[1:]); err != nil {
				return nil, err
			}
			if *role == "" || *binding == "" || *expected == "" || fs.NArg() != 0 {
				return nil, verbs.Usagef("%s", usage)
			}
			return verbs.RoutingSet(*role, *binding, *expected)
		}
		return nil, verbs.Usagef("%s", usage)
	case "logs":
		// Pure host-side read: the log tail for the System Logs page.
		// Unit and stream select from a fixed table, and the one lane that
		// names a file — `mc logs run <id>` — takes an id, never a path.
		//
		// Every lane fences host scope BEFORE any file is opened, and this verb
		// stays in the runLocal carve-out in run() for the reason LogsTailRun
		// documents: delegated into the helper it would answer for an MC_HOME
		// that does not exist there, at exit 0.
		usage := "usage: mc logs <resident|dashboard|agentsview|mc> [--stream stdout|stderr|events|debug] [--tail N] [--format text|json]\n" +
			"       mc logs run <run-id> [--tail N] [--format text|json]\n" +
			"       mc logs runs --list"
		if len(rest) < 1 || strings.HasPrefix(rest[0], "-") {
			return nil, verbs.Usagef("%s", usage)
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc logs"); err != nil {
			return nil, err
		}
		switch rest[0] {
		case "run":
			if len(rest) < 2 || strings.HasPrefix(rest[1], "-") {
				return nil, verbs.Usagef("%s", usage)
			}
			fs := newFlags("mc logs run")
			tail := fs.Int("tail", 0, "line count (default 200, clamped)")
			format := fs.String("format", "text", "record rendering (text|json)")
			if err := parse(fs, rest[2:]); err != nil {
				return nil, err
			}
			return verbs.LogsTailRun(rest[1], *format, *tail)
		case "runs":
			fs := newFlags("mc logs runs")
			list := fs.Bool("list", false, "name the runs that have diagnostics")
			if err := parse(fs, rest[1:]); err != nil {
				return nil, err
			}
			if !*list {
				return nil, verbs.Usagef("%s", usage)
			}
			return verbs.LogsRunsList()
		}
		unit := rest[0]
		fs := newFlags("mc logs")
		defaultStream := "stderr"
		if unit == "agentsview" {
			// AgentsView writes its operational log to the debug stream.
			defaultStream = "debug"
		}
		stream := fs.String("stream", defaultStream, "log stream (stdout|stderr|events|debug)")
		tail := fs.Int("tail", 0, "line count (default 200, clamped)")
		format := fs.String("format", "text", "record rendering (text|json)")
		if err := parse(fs, rest[1:]); err != nil {
			return nil, err
		}
		return verbs.LogsTail(unit, *stream, *format, *tail)
	case "health":
		// Host-plane health from FILES: no spine, no Docker, no helper. See
		// verbs/health.go for why that constraint is the verb rather than a
		// limitation of it.
		fs := newFlags("mc health")
		tail := fs.Int("tail", 0, "records per component to judge (default 200, clamped)")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc health"); err != nil {
			return nil, err
		}
		return verbs.Health(*tail)
	}
	return nil, verbs.Usagef("unknown verb %q", verb)
}

// withSpine opens MC_SPINE and runs fn.
func withSpine(fn func(db *sql.DB) (any, error)) (any, error) {
	db, err := verbs.OpenSpine(helperSpinePath())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return fn(db)
}

// withSpineRead opens MC_SPINE through the one shared read-only path and runs
// fn. Every `mc <record> get/list` read goes through here, so a read verb
// never needs a WAL/SHM sidecar created and works against a spine on a
// read-only mount. The connection carries query_only, so a write routed
// through this helper fails loudly instead of quietly weakening the read path.
func withSpineRead(fn func(db *sql.DB) (any, error)) (any, error) {
	db, err := verbs.OpenSpineRead(helperSpinePath())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return fn(db)
}

// repeatedFlag collects a repeatable string flag.
type repeatedFlag []string

func (r *repeatedFlag) String() string { return strings.Join(*r, ",") }
func (r *repeatedFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// flagSet reports whether the parsed arguments named the flag.
func flagSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == name })
	return set
}

func parse(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return verbs.Usagef("%s: %v", fs.Name(), err)
	}
	if fs.NArg() != 0 {
		return verbs.Usagef("%s: unexpected argument %q", fs.Name(), fs.Arg(0))
	}
	return nil
}

// positional peels the verb's one leading positional argument (Go's flag
// package stops at the first non-flag, so `mc complete <task> --run …`
// parses positional-first).
func positional(name string, args []string) (string, []string, error) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", nil, verbs.Usagef("%s: missing argument", name)
	}
	return args[0], args[1:], nil
}

func positionalID(name string, args []string) (int64, []string, error) {
	s, rest, err := positional(name, args)
	if err != nil {
		return 0, nil, err
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, nil, verbs.Usagef("%s: bad id %q", name, s)
	}
	return id, rest, nil
}

func parseOnboardArgs(args []string) (verbs.OnboardArgs, error) {
	a := verbs.OnboardArgs{Spine: helperSpinePath()}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		a.Section = args[0]
		args = args[1:]
	}
	fs := newFlags("mc onboard")
	fs.BoolVar(&a.Smoke, "smoke", false, "run the full-pipeline smoke")
	fs.Int64Var(&a.SmokeApproveTask, "smoke-approve-task", 0,
		"approve this exact smoke packet task after it is ready")
	fs.BoolVar(&a.SmokeReset, "smoke-reset", false,
		"retire the registered smoke generation instead of resuming it")
	fs.StringVar(&a.Project, "project", "", "Project id")
	fs.StringVar(&a.WorkspaceRoot, "workspace-root", "", "Project workspace root")
	fs.StringVar(&a.ProjectTitle, "title", "", "Project display title; the id when absent")
	fs.BoolVar(&a.ProjectPaused, "paused", false, "register the Project paused")
	fs.BoolVar(&a.ProjectVisionSetup, "project-vision-setup", false, "register the Project for Vision setup")
	fs.StringVar(&a.ProjectGitAuthorName, "project-git-author-name", "",
		"optional Project commit author name override")
	fs.StringVar(&a.ProjectGitAuthorEmail, "project-git-author-email", "",
		"optional Project commit author email override")
	fs.BoolVar(&a.AcceptGeneratedSandbox, "accept-generated-sandbox", false,
		"accept the reviewed deny-by-default Project sandbox profile")
	fs.StringVar(&a.ProjectVisionAnswer, "project-vision", "",
		"when VISION.md is absent: create a starter now or write it later; work requires the completed file")
	fs.StringVar(&a.RuntimeBindings, "runtime-bindings", "", "comma-separated runtime bindings")
	fs.BoolVar(&a.AcquireRuntimeAuth, "acquire", false, "run an isolated Culler sign-in for each subscription binding")
	fs.BoolVar(&a.ConfirmLiveSpend, "confirm-live-spend", false, "confirm the provider spend of the live acceptance for every enabled binding")
	fs.BoolVar(&a.AcceptDefaultRouting, "accept-default-routing", false, "accept the recommended role routing")
	fs.StringVar(&a.RoutingFile, "routing-file", "", "absolute path to the requested routing table")
	fs.BoolVar(&a.ActivateSupervision, "activate", false, "install and load prepared native supervision (operator-present)")
	fs.BoolVar(&a.ReconfigureSupervision, "reconfigure", false,
		"stop loaded units and re-apply a changed config.toml (operator-present)")
	fs.BoolVar(&a.AllowDatabaseReset, "allow-database-reset", false, "replace the deployment database with an empty database (deployment administration)")
	fs.BoolVar(&a.RestoreLatest, "restore-latest", false, "restore the newest matching snapshot into a lost spine")
	fs.StringVar(&a.ReleaseSource, "release-source", "", "repository runner source (install.sh only)")
	fs.StringVar(&a.HostReleaseSource, "host-release-source", "", "repository root for native host payloads (install.sh only)")
	fs.IntVar(&a.TimeoutMinutes, "timeout-minutes", 0, "lease timeout")
	fs.IntVar(&a.GraceMinutes, "grace-minutes", 0, "lease grace")
	fs.IntVar(&a.HeartbeatIntervalS, "heartbeat-interval-s", 0, "heartbeat interval")
	fs.IntVar(&a.SpawnGraceS, "spawn-grace-s", 0, "first-heartbeat watchdog")
	fs.IntVar(&a.HardDeadlineMinutes, "hard-deadline-minutes", 0, "non-renewable hard deadline")
	fs.IntVar(&a.CollieIdleTimeoutS, "collie-idle-timeout-s", 0, "Collie conversation idle timeout")
	fs.IntVar(&a.ReviewWIPCap, "review-wip-cap", 0, "maximum live review packets")
	fs.BoolVar(&a.TunablesConfirmed, "accept-default-tunables", false, "accept recommended tunable defaults")
	fs.BoolVar(&a.AcceptDefaultGitIdentity, "accept-default-git-identity", false,
		"accept Culler <mc@localhost>")
	fs.StringVar(&a.GitAuthorName, "git-author-name", "", "role commit author name")
	fs.StringVar(&a.GitAuthorEmail, "git-author-email", "", "role commit author email")
	fs.BoolVar(&a.AcceptDefaultDashboardBind, "accept-default-dashboard-bind", false,
		"accept dashboard bind 127.0.0.1:7333")
	fs.StringVar(&a.DashboardBind, "dashboard-bind", "", "dashboard host:port")
	fs.StringVar(&a.DashboardAuthTokenFile, "dashboard-auth-token-file", "",
		"owner-only dashboard bearer-token source")
	fs.StringVar(&a.DiscordMode, "discord-mode", "", "disabled|enabled")
	fs.StringVar(&a.DiscordBotTokenFile, "discord-bot-token-file", "",
		"owner-only Discord bot-token source")
	fs.StringVar(&a.DiscordGuildID, "discord-guild-id", "", "Discord guild id")
	fs.StringVar(&a.DiscordChannelID, "discord-channel-id", "", "Discord channel id")
	fs.StringVar(&a.DiscordOperatorIDs, "discord-operator-ids", "",
		"comma-separated allowed Discord user ids")
	fs.BoolVar(&a.DiscordTestConfirmed, "confirm-discord-test", false,
		"confirm the posted Discord onboarding test message")
	if err := parse(fs, args); err != nil {
		return verbs.OnboardArgs{}, err
	}
	provided := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { provided[f.Name] = true })
	if a.ActivateSupervision && a.Section != "" && a.Section != "supervision" {
		return verbs.OnboardArgs{}, verbs.Usagef("mc __onboard --activate is valid only for the supervision section")
	}
	if a.AllowDatabaseReset && a.Section != "home" {
		return verbs.OnboardArgs{}, verbs.Usagef("database reset authorization requires the home section")
	}
	if a.RestoreLatest && a.Section != "" && a.Section != "home" {
		return verbs.OnboardArgs{}, verbs.Usagef("mc onboard --restore-latest is valid only for the home section")
	}
	if provided["smoke-approve-task"] && (a.SmokeApproveTask <= 0 || !a.Smoke) {
		return verbs.OnboardArgs{}, verbs.Usagef(
			"mc __onboard --smoke-approve-task requires --smoke and a positive task id")
	}
	if a.SmokeReset && !a.Smoke {
		return verbs.OnboardArgs{}, verbs.Usagef("mc __onboard --smoke-reset requires --smoke")
	}
	if a.SmokeReset && provided["smoke-approve-task"] {
		// Retiring a generation and deciding its packet are opposite intents;
		// silently honouring one would write the decision the other refuses to.
		return verbs.OnboardArgs{}, verbs.Usagef(
			"mc __onboard --smoke-reset cannot be combined with --smoke-approve-task")
	}
	if a.AcceptDefaultRouting && a.RoutingFile != "" {
		return verbs.OnboardArgs{}, verbs.Usagef("mc onboard routing accepts only one of --accept-default-routing or --routing-file")
	}
	if provided["review-wip-cap"] && a.ReviewWIPCap <= 0 {
		return verbs.OnboardArgs{}, verbs.Usagef("mc onboard --review-wip-cap must be positive")
	}
	hasTunableOverride := a.ReviewWIPCap > 0 || a.TimeoutMinutes > 0 ||
		a.GraceMinutes > 0 || a.HeartbeatIntervalS > 0 ||
		a.SpawnGraceS > 0 || a.HardDeadlineMinutes > 0
	if a.TunablesConfirmed && hasTunableOverride {
		return verbs.OnboardArgs{}, verbs.Usagef("--accept-default-tunables cannot be combined with tunable overrides")
	}
	hasGitIdentity := a.GitAuthorName != "" || a.GitAuthorEmail != ""
	if hasGitIdentity && (a.GitAuthorName == "" || a.GitAuthorEmail == "") {
		return verbs.OnboardArgs{}, verbs.Usagef("--git-author-name and --git-author-email are required together")
	}
	if a.AcceptDefaultGitIdentity && hasGitIdentity {
		return verbs.OnboardArgs{}, verbs.Usagef("--accept-default-git-identity cannot be combined with custom Git identity")
	}
	hasProjectGitIdentity := provided["project-git-author-name"] ||
		provided["project-git-author-email"]
	if hasProjectGitIdentity &&
		(!provided["project-git-author-name"] || !provided["project-git-author-email"] ||
			a.ProjectGitAuthorName == "" || a.ProjectGitAuthorEmail == "") {
		return verbs.OnboardArgs{}, verbs.Usagef(
			"--project-git-author-name and --project-git-author-email are required together")
	}
	if a.ProjectVisionAnswer != "" && a.ProjectVisionAnswer != verbs.ProjectVisionAnswerCreate &&
		a.ProjectVisionAnswer != verbs.ProjectVisionAnswerLater {
		return verbs.OnboardArgs{}, verbs.Usagef("--project-vision must be create|later")
	}
	if a.AcceptDefaultDashboardBind && a.DashboardBind != "" {
		return verbs.OnboardArgs{}, verbs.Usagef("--accept-default-dashboard-bind cannot be combined with --dashboard-bind")
	}
	if a.DiscordMode != "" && a.DiscordMode != "disabled" && a.DiscordMode != "enabled" {
		return verbs.OnboardArgs{}, verbs.Usagef("--discord-mode must be disabled|enabled")
	}
	hasDiscordFields := a.DiscordBotTokenFile != "" || a.DiscordGuildID != "" ||
		a.DiscordChannelID != "" || a.DiscordOperatorIDs != "" || a.DiscordTestConfirmed
	if a.DiscordMode == "disabled" && hasDiscordFields {
		return verbs.OnboardArgs{}, verbs.Usagef("disabled Discord cannot receive Discord credential/id/test fields")
	}
	if a.DiscordMode == "" && hasDiscordFields {
		return verbs.OnboardArgs{}, verbs.Usagef("Discord fields require --discord-mode enabled")
	}
	for _, item := range []struct {
		name  string
		value int
	}{
		{"timeout-minutes", a.TimeoutMinutes},
		{"grace-minutes", a.GraceMinutes},
		{"heartbeat-interval-s", a.HeartbeatIntervalS},
		{"spawn-grace-s", a.SpawnGraceS},
		{"hard-deadline-minutes", a.HardDeadlineMinutes},
		{"collie-idle-timeout-s", a.CollieIdleTimeoutS},
	} {
		if item.value < 0 {
			return verbs.OnboardArgs{}, verbs.Usagef("mc onboard --%s must be non-negative", item.name)
		}
	}
	return a, nil
}

// cmdOnboard is the agent-facing wizard entry: setup sections only.
// The install-time sections, release flags, and the smoke are refused here
// and live on `__onboard`, which only the front doors invoke.
func cmdOnboard(args []string) (any, error) {
	a, err := parseOnboardArgs(args)
	if err != nil {
		return nil, err
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	return verbs.OnboardSetup(id, a)
}

// cmdOnboardFull is the private front-door wizard (`mc __onboard`): every
// onboarding section in order, release install, and the smoke — unlisted on
// the agent surface and refused outside host scope.
func cmdOnboardFull(args []string) (any, error) {
	a, err := parseOnboardArgs(args)
	if err != nil {
		return nil, err
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	return verbs.Onboard(id, a)
}

func cmdInit(args []string) (any, error) {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	if err := verbs.RequireHostScope(id, "mc __init"); err != nil {
		return nil, err
	}
	fs := newFlags("mc __init")
	a := verbs.InitArgs{}
	fs.StringVar(&a.Spine, "spine", "", "spine path to provision")
	fs.StringVar(&a.Project, "project", "", "project id")
	fs.StringVar(&a.WorkspaceRoot, "workspace-root", "", "workspace root path")
	fs.StringVar(&a.ArtifactRoot, "artifact-root", "", "durable artifact plane for the seeded profile")
	fs.IntVar(&a.TimeoutMinutes, "timeout-minutes", 0, "lease timeout")
	fs.IntVar(&a.GraceMinutes, "grace-minutes", 0, "lease grace")
	fs.IntVar(&a.HeartbeatIntervalS, "heartbeat-interval-s", 0, "heartbeat interval")
	fs.IntVar(&a.SpawnGraceS, "spawn-grace-s", 0, "first-heartbeat watchdog")
	fs.IntVar(&a.HardDeadlineMinutes, "hard-deadline-minutes", 0, "non-renewable hard deadline")
	fs.IntVar(&a.ReviewWIPCap, "review-wip-cap", 0, "maximum live review packets per Project")
	fs.IntVar(&a.CollieIdleTimeoutS, "collie-idle-timeout-s", 0, "Collie session idle timeout")
	if err := parse(fs, args); err != nil {
		return nil, err
	}
	if a.Spine == "" {
		a.Spine = helperSpinePath()
	}
	return verbs.Init(a)
}

func cmdTask(args []string) (any, error) {
	if len(args) == 0 {
		return nil, verbs.Usagef("usage: mc task add|get|list|block|unblock|resume|cancel|interrupt …")
	}
	switch args[0] {
	case "add":
		title, rest, err := positional("mc task add", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc task add")
		project := fs.String("project", "", "project id")
		description := fs.String("description", "", "task description")
		priority := fs.Int("priority", -100, "priority (-1..3)")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		var pri *int
		if *priority != -100 {
			pri = priority
		}
		idn, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.TaskAdd(db, idn, title, *project, *description, pri)
		})
	case "get":
		if len(args) != 2 {
			return nil, verbs.Usagef("usage: mc task get <id>")
		}
		id, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return nil, verbs.Usagef("mc task get: bad id %q", args[1])
		}
		idn, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.TaskRead(db, idn, id) })
	case "list":
		fs := newFlags("mc task list")
		all := fs.Bool("all", false, "include archived rows (default: live work only)")
		project := fs.String("project", "", "restrict to one Project")
		if err := fs.Parse(args[1:]); err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) {
			return verbs.TaskList(db, *all, *project)
		})
	case "block":
		id, rest, err := positionalID("mc task block", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc task block")
		reason := fs.String("reason", "", "operator decision required")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if strings.TrimSpace(*reason) == "" {
			return nil, verbs.Usagef("Block requires a reason.")
		}
		idn, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.TaskBlock(db, idn, id, *reason)
		})
	case "unblock", "resume":
		id, rest, err := positionalID("mc task "+args[0], args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc task " + args[0])
		reason := fs.String("reason", "", "the reason for decision resolution or task resume")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if strings.TrimSpace(*reason) == "" {
			return nil, verbs.Usagef("mc task %s requires a reason.", args[0])
		}
		idn, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			if args[0] == "resume" {
				return verbs.TaskResume(db, idn, id, *reason)
			}
			return verbs.TaskUnblock(db, idn, id, *reason)
		})
	case "cancel":
		id, rest, err := positionalID("mc task cancel", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc task cancel")
		reason := fs.String("reason", "", "why the task is cancelled (required)")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		idn, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.TaskCancel(db, idn, id, *reason)
		})
	case "interrupt":
		id, rest, err := positionalID("mc task interrupt", args[1:])
		if err != nil {
			return nil, err
		}
		if len(rest) != 0 {
			return nil, verbs.Usagef("mc task interrupt: unexpected argument %q", rest[0])
		}
		idn, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.TaskInterrupt(db, idn, id)
		})
	}
	return nil, verbs.Usagef("unknown subverb: mc task %s", args[0])
}

func cmdProject(args []string, stdin io.Reader) (any, error) {
	if len(args) == 0 {
		return nil, verbs.Usagef("usage: mc project add|list|pause|resume|archive …\n" +
			"mc project add --sandbox-profile <id> develops that profile's workspace root with git: " +
			"an existing repository there is adopted, and if there is no repository Culler runs `git init` in it")
	}
	switch args[0] {
	case "role-show", "role-save", "role-unblock":
		return cmdProjectRole(args, stdin)
	case "container-targets":
		if len(args) != 2 {
			return nil, verbs.Usagef("Project container inspection requires one Project.")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.ProjectContainerTargets(db, id, args[1]) })
	case "delete-preview", "delete":
		projectID, rest, err := positional("mc project "+args[0], args[1:])
		if err != nil {
			return nil, err
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err = verbs.RequireHostScope(id, "project."+args[0]); err != nil {
			return nil, err
		}
		if args[0] == "delete-preview" {
			if len(rest) != 0 {
				return nil, verbs.Usagef("usage: mc project delete-preview <project>")
			}
			return withSpineRead(func(db *sql.DB) (any, error) { return verbs.ProjectDeletePreview(db, id, projectID) })
		}
		if len(rest) != 1 || rest[0] != "--confirm" {
			return nil, verbs.Usagef("Project deletion requires explicit confirmation in the Projects page.")
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.ProjectDelete(db, id, projectID) })
	case "list":
		if len(args) != 1 {
			return nil, verbs.Usagef("usage: mc project list")
		}
		return withSpineRead(func(db *sql.DB) (any, error) { return verbs.ProjectList(db) })
	case "add":
		projectID, rest, err := positional("mc project add", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc project add")
		a := verbs.ProjectAddArgs{ID: projectID}
		fs.StringVar(&a.Title, "title", "", "display title")
		fs.BoolVar(&a.Paused, "paused", false, "create the Project paused")
		fs.StringVar(&a.Jurisdiction, "jurisdiction", "", "jurisdiction description")
		fs.StringVar(&a.SandboxProfile, "sandbox-profile", "", "sandbox profile id")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		out, err := withSpine(func(db *sql.DB) (any, error) { return verbs.ProjectAdd(db, id, a) })
		// Host scope only: a Collie's in-container add cannot reach the host
		// store, and the resident creates that Project's directories the
		// first time it spawns a run there.
		if err == nil && id == nil {
			extendSkillStoreOnHost(projectID, os.Stderr)
		}
		return out, err
	case "pause", "resume", "archive":
		projectID, rest, err := positional("mc project "+args[0], args[1:])
		if err != nil {
			return nil, err
		}
		if len(rest) != 0 {
			return nil, verbs.Usagef("mc project %s: unexpected argument %q", args[0], rest[0])
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		target := "paused"
		if args[0] == "resume" {
			target = "active"
		} else if args[0] == "archive" {
			target = "archived"
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.ProjectSetStatus(db, id, projectID, target)
		})
	}
	return nil, verbs.Usagef("unknown subverb: mc project %s", args[0])
}

func cmdPacket(args []string) (any, error) {
	if len(args) == 0 {
		return nil, verbs.Usagef("usage: mc packet decide|list …")
	}
	switch args[0] {
	case "decide":
		task, rest, err := positionalID("mc packet decide", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc packet decide")
		approve := fs.Bool("approve", false, "approve the packet")
		revise := fs.Bool("revise", false, "send the packet back for revision")
		cancel := fs.Bool("cancel", false, "cancel the packet")
		reason := fs.String("reason", "", "decision reason")
		packet := fs.Int64("packet", 0, "the reviewed packet")
		version := fs.Int64("version", 0, "the submitted version")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		decision := ""
		n := 0
		for name, set := range map[string]bool{"approve": *approve, "revise": *revise, "cancel": *cancel} {
			if set {
				decision = name
				n++
			}
		}
		if n != 1 {
			return nil, verbs.Usagef("mc packet decide requires exactly one of --approve, --revise, --cancel")
		}
		idn, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.PacketDecide(db, idn, task, decision, *reason, *packet, *version)
		})
	case "list":
		// Through the shared `parse` helper, not a bare `fs.Parse`: it is what
		// turns a bad flag into a USAGE error (exit 2) rather than a domain
		// rejection, and it is what refuses a trailing positional instead of
		// silently ignoring it.
		fs := newFlags("mc packet list")
		live := fs.Bool("live", false, "only packets still awaiting a decision (default: every packet ever)")
		task := fs.Int64("task", 0, "only the latest packet of this task")
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		if flagSet(fs, "task") && *task < 1 {
			return nil, verbs.Usagef("mc packet list --task requires a positive task identifier")
		}
		return withSpineRead(func(db *sql.DB) (any, error) {
			return verbs.PacketList(db, verbs.PacketSelection{Live: *live, Task: *task})
		})
	}
	return nil, verbs.Usagef("unknown subverb: mc packet %s", args[0])
}

// cmdActivity reads the Activity stream with optional filters.
func cmdActivity(args []string) (any, error) {
	if len(args) == 0 || args[0] != "list" {
		return nil, verbs.Usagef("usage: mc activity list [--kind <kind>] [--limit <n>] [--project <id>]")
	}
	fs := newFlags("mc activity list")
	kind := fs.String("kind", "", "restrict to one activity kind")
	project := fs.String("project", "", "restrict to one Project")
	limit := fs.Int("limit", 0, "maximum rows, newest first")
	if err := fs.Parse(args[1:]); err != nil {
		return nil, err
	}
	return withSpineRead(func(db *sql.DB) (any, error) {
		return verbs.ActivityList(db, *kind, *limit, *project)
	})
}

func cmdComplete(args []string) (any, error) {
	if len(args) == 0 || len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return nil, verbs.Usagef("usage: mc complete [task_id] --run <run_id>\nUse one terminal: --status worked|packaged, --checkpoint, --needs-operator, or --infra.\nProposal runs omit task_id. The --needs-operator and --infra terminals require --reason.\nThe Scribe omits task_id and uses --commit with --wiki-evidence, or --infra.")
	}

	var task int64
	rest := args
	var err error
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		task, rest, err = positionalID("mc complete", args)
		if err != nil {
			return nil, err
		}
	}
	fs := newFlags("mc complete")
	a := verbs.CompleteArgs{Task: task}
	fs.BoolVar(&a.Checkpoint, "checkpoint", false, "retain the commit for another Worker run")
	fs.StringVar(&a.Run, "run", "", "fencing token")
	fs.StringVar(&a.Status, "status", "", "worked|packaged")
	fs.StringVar(&a.Commit, "commit", "", "Worker and Scribe hand-in: the exact HEAD commit of the isolated repository")
	fs.StringVar(&a.WikiEvidence, "wiki-evidence", "", "Scribe: the path of the OpenWiki completion receipt")
	fs.StringVar(&a.Outputs, "outputs", "", "output artifact path")
	fs.StringVar(&a.Thesis, "thesis", "", "Packager: the packet's 30-second thesis")
	reason := fs.String("reason", "", "terminal reason")
	affected := fs.String("affected", "", "affected proposal identifiers, separated by commas")
	needsOperator := fs.Bool("needs-operator", false, "block for an operator decision")
	infra := fs.Bool("infra", false, "report a task run failure or Project infrastructure failure")
	correctionCount := fs.Int("correction-count", -1, "reserved; verifier verdict owns correction arithmetic")
	if err := parse(fs, rest); err != nil {
		return nil, err
	}
	if *correctionCount != -1 {
		return nil, verbs.Domainf("--correction-count is written only by mc verifier verdict; mc complete cannot own the quality budget")
	}
	a.Reason = *reason
	if *affected != "" {
		if !*needsOperator || task != 0 {
			return nil, verbs.Usagef("Affected proposals require a Project proposal decision.")
		}
		for _, raw := range strings.Split(*affected, ",") {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 1 {
				return nil, verbs.Usagef("Affected proposals require positive identifiers.")
			}
			a.Affected = append(a.Affected, value)
		}
	}
	a.NeedsOperator = *needsOperator
	a.Infra = *infra
	if a.Run == "" {
		return nil, verbs.Usagef("mc complete requires --run (the fencing token)")
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	return withSpine(func(db *sql.DB) (any, error) { return verbs.Complete(db, id, a) })
}

// batchReader validates --batch - and returns the payload stream: batch
// payloads arrive as JSON on stdin.
func batchReader(batch string, stdin io.Reader) (io.Reader, error) {
	if batch != "-" {
		return nil, verbs.Usagef("--batch supports only '-' (JSON on stdin)")
	}
	return stdin, nil
}

func cmdArbiter(args []string, stdin io.Reader) (any, error) {
	if len(args) == 0 || args[0] != "decide" {
		return nil, verbs.Usagef("usage: mc arbiter decide --run <id> --batch -")
	}
	fs := newFlags("mc arbiter decide")
	run := fs.String("run", "", "fencing token")
	batch := fs.String("batch", "", "batch payload source")
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	if *run == "" {
		return nil, verbs.Usagef("mc arbiter decide requires --run")
	}
	r, err := batchReader(*batch, stdin)
	if err != nil {
		return nil, err
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	return withSpine(func(db *sql.DB) (any, error) { return verbs.ArbiterDecide(db, id, *run, r) })
}

func cmdStrategist(args []string, stdin io.Reader) (any, error) {
	if len(args) == 0 || args[0] != "propose" {
		return nil, verbs.Usagef("usage: mc strategist propose --run <id> --batch -")
	}
	fs := newFlags("mc strategist propose")
	run := fs.String("run", "", "fencing token")
	batch := fs.String("batch", "", "batch payload source")
	if err := parse(fs, args[1:]); err != nil {
		return nil, err
	}
	if *run == "" {
		return nil, verbs.Usagef("mc strategist propose requires --run")
	}
	r, err := batchReader(*batch, stdin)
	if err != nil {
		return nil, err
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	return withSpine(func(db *sql.DB) (any, error) { return verbs.StrategistPropose(db, id, *run, r) })
}

func cmdAttachment(args []string) (any, error) {
	if len(args) == 0 {
		return nil, verbs.Usagef("usage: mc attachment ingress-enqueue|ingress-queued …")
	}
	switch args[0] {
	case "ingress-enqueue":
		sessionID, rest, err := positional("mc attachment ingress-enqueue", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc attachment ingress-enqueue")
		bindingID := fs.Int64("binding", 0, "exact active Collie binding id")
		surface := fs.String("surface", "", "discord|dashboard|cli")
		messageID := fs.String("message-id", "", "stable native surface message id")
		body := fs.String("body", "", "bounded inbound text")
		manifest := fs.String("manifest", "", "complete sealed attachment manifest JSON")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if *bindingID <= 0 || *surface == "" || *messageID == "" || *manifest == "" {
			return nil, verbs.Usagef("mc attachment ingress-enqueue requires --binding, --surface, --message-id, and --manifest")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.AttachmentIngressEnqueue(db, id, verbs.AttachmentIngressEnqueueArgs{
				Session: sessionID, BindingID: *bindingID, Surface: *surface,
				ExternalMessageID: *messageID, Body: *body, Manifest: *manifest,
			})
		})
	case "ingress-queued":
		fs := newFlags("mc attachment ingress-queued")
		surface := fs.String("surface", "", "discord|dashboard|cli")
		after := fs.String("after", "0", "stable ingress id cursor")
		limit := fs.Int("limit", 128, "bounded scan size (1..128)")
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		if *surface == "" {
			return nil, verbs.Usagef("mc attachment ingress-queued requires --surface")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		// Pure read (native-recovery pagination, SELECT only): the shared
		// read-only path serves it like every other read verb.
		return withSpineRead(func(db *sql.DB) (any, error) {
			return verbs.AttachmentIngressQueued(db, id, verbs.AttachmentIngressQueuedArgs{
				Surface: *surface, After: *after, Limit: *limit,
			})
		})
	default:
		return nil, verbs.Usagef("usage: mc attachment ingress-enqueue|ingress-queued …")
	}
}

func cmdCollie(args []string) (any, error) {
	if len(args) == 0 {
		return nil, verbs.Usagef("usage: mc collie start|bind|send|claim|reply|list|history|resume|end …")
	}
	switch args[0] {
	case "start":
		start, err := parseCollieStartArgs(args[1:])
		if err != nil {
			return nil, err
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.CollieStart(db, id, start)
		})

	case "title":
		session, rest, err := positional("mc collie title", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc collie title")
		title := fs.String("title", "", "conversation title")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.CollieTitle(db, id, session, *title) })

	case "delete":
		if len(args) != 2 {
			return nil, verbs.Usagef("mc collie delete requires one session")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.CollieDelete(db, id, args[1]) })

	case "bind":
		sessionID, rest, err := positional("mc collie bind", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc collie bind")
		from := fs.String("from", "", "surface:channel_ref")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if *from == "" {
			return nil, verbs.Usagef("mc collie bind requires --from <surface:channel_ref>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.CollieBind(db, id, sessionID, *from)
		})

	case "resume":
		sessionID, rest, err := positional("mc collie resume", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc collie resume")
		from := fs.String("from", "", "surface:channel_ref")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if *from == "" {
			return nil, verbs.Usagef("mc collie resume requires --from <surface:channel_ref>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.CollieResume(db, id, sessionID, *from)
		})

	case "list":
		if len(args) != 1 {
			return nil, verbs.Usagef("usage: mc collie list")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) {
			return verbs.CollieList(db, id)
		})

	case "send":
		sessionID, rest, err := positional("mc collie send", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc collie send")
		sourceMessage := fs.String("source-message", "", "Discord source message identifier")
		from := fs.String("from", "", "origin surface:channel_ref")
		body := fs.String("body", "", "inbound text")
		attachments := fs.String("attachments", "", "reserved: typed attachment publication only")
		ingressAttempt := fs.String("ingress-attempt", "", "exact materialized attachment ingress attempt")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if *from == "" {
			return nil, verbs.Usagef("mc collie send requires --from <surface:channel_ref>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			if *ingressAttempt != "" {
				if *attachments != "" || *sourceMessage != "" {
					return nil, verbs.Usagef(
						"mc collie send --ingress-attempt cannot be combined with --attachments",
					)
				}
				return verbs.AttachmentIngressConsume(
					db, id, verbs.AttachmentIngressConsumeArgs{
						Session: sessionID, From: *from, Body: *body,
						AttemptID: *ingressAttempt,
					},
				)
			}
			return verbs.CollieSend(db, id, verbs.CollieSendArgs{
				Session: sessionID, From: *from, Body: *body, Attachments: *attachments, SourceMessage: *sourceMessage,
			})
		})

	case "launch-failure", "launch-check":
		session, rest, err := positional("Collie launch", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("Collie launch")
		launch := fs.String("launch", "", "launch identity")
		connection := fs.String("connection", "", "connection")
		authorization := fs.String("authorization", "", "authorization identity")
		profile := fs.String("profile", "", "agent profile")
		revision := fs.String("profile-revision", "", "profile revision")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if args[0] == "launch-check" {
			if err := verbs.RequireHostScope(id, "Collie launch check"); err != nil {
				return nil, err
			}
			required, err := systemRequirements()
			if err != nil {
				return nil, err
			}
			valid := false
			for _, current := range required.Connections {
				if current.ID == *connection && current.AuthorizationID == *authorization && current.Profiles[*profile] == *revision {
					valid = true
				}
			}
			if !valid {
				return nil, verbs.Domainf("The connection changed before the Collie launch.")
			}
			return systemSpineCall("collie-launch-check", collieLaunchRequest{Session: session, Launch: *launch, Connection: *connection, Authorization: *authorization, Profile: *profile, ProfileRevision: *revision})
		}
		return withSpine(func(db *sql.DB) (any, error) {
			if args[0] == "launch-failure" {
				return verbs.CollieLaunchFailure(db, id, session, *launch)
			}
			return verbs.CollieLaunchCheck(db, id, session, *launch, *connection, *authorization, *profile, *revision)
		})

	case "model-response":
		if len(args) != 2 {
			return nil, verbs.Usagef("The model response requires one reply attempt identity.")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.CollieModelResponse(db, id, args[1]) })
	case "reply-failure":
		attempt, rest, err := positional("reply failure", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("reply failure")
		proxyRequest := fs.String("proxy-request-id", "", "host provider request identity")
		responseStatus := fs.Int("http-status", 0, "provider response status")
		providerCode := fs.String("provider-error-code", "", "recognized provider code")
		networkError := fs.String("network-error", "", "connect|reset|timeout|tls|other for a request without a provider response")
		condition := fs.String("condition", "", "observed failure")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.CollieReplyFailure(db, id, attempt, *condition, domain.InfrastructureFailureReport{ProxyRequestID: *proxyRequest, HTTPStatus: optionalResponseStatus(*responseStatus), ProviderErrorCode: *providerCode, NetworkError: *networkError})
		})
	case "reply-retry":
		session, rest, err := positional("reply retry", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("reply retry")
		message := fs.Int64("message", 0, "message")
		key := fs.String("request", "", "request identity")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.CollieReplyRetry(db, id, session, *message, *key) })

	case "claim":
		sessionID, rest, err := positional("mc collie claim", args[1:])
		if err != nil {
			return nil, err
		}
		if len(rest) != 0 {
			return nil, verbs.Usagef("mc collie claim: unexpected argument %q", rest[0])
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.CollieClaim(db, id, sessionID)
		})

	case "reply":
		sessionID, rest, err := positional("mc collie reply", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc collie reply")
		attempt := fs.String("attempt", "", "reply attempt identity")
		to := fs.Int64("to", 0, "claimed inbound message id")
		body := fs.String("body", "", "reply text")
		attachments := fs.String("attachments", "", "reserved: typed attachment publication only")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if *to <= 0 {
			return nil, verbs.Usagef("mc collie reply requires --to <inbound message id>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.CollieReply(db, id, verbs.CollieReplyArgs{
				Session: sessionID, To: *to, Body: *body, Attempt: *attempt, Attachments: *attachments,
			})
		})

	case "draft":
		sessionID, rest, err := positional("mc collie draft", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc collie draft")
		attempt := fs.String("attempt", "", "reply attempt identity")
		to := fs.Int64("to", 0, "claimed inbound message id")
		body := fs.String("body", "", "provisional reply text so far")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if *to <= 0 {
			return nil, verbs.Usagef("mc collie draft requires --to <inbound message id>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.CollieDraft(db, id, verbs.CollieDraftArgs{
				Session: sessionID, To: *to, Body: *body, Attempt: *attempt,
			})
		})

	case "packet-context":
		sessionID, rest, err := positional("mc collie packet-context", args[1:])
		if err != nil {
			return nil, err
		}
		if len(rest) != 0 {
			return nil, verbs.Usagef("mc collie packet-context: unexpected argument %q", rest[0])
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) {
			return verbs.ColliePacketContext(db, id, sessionID)
		})

	case "history":
		sessionID, rest, err := positional("mc collie history", args[1:])
		if err != nil {
			return nil, err
		}
		// The CLI read channel: `--follow` returns when the session produces
		// its next reply, so the caller needs no delivery loop of its own.
		// Bounded, and expiry is an ordinary unchanged transcript.
		fs := newFlags("mc collie history")
		follow := fs.Bool("follow", false, "wait for the session's next reply before returning")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) {
			return verbs.CollieHistory(db, id, sessionID, *follow)
		})

	case "end":
		sessionID, rest, err := positional("mc collie end", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc collie end")
		reason := fs.String("reason", "", "end reason")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if *reason == "" {
			return nil, verbs.Usagef("mc collie end requires --reason")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.CollieEnd(db, id, sessionID, *reason)
		})

	case "launch-bind":
		// Resident-private pre-start bind receipt.
		sessionID, rest, err := positional("mc collie launch-bind", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc collie launch-bind")
		launch := fs.String("launch", "", "16-hex current launch id")
		containerID := fs.String("container-id", "", "64-hex Docker container id")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if *launch == "" || *containerID == "" {
			return nil, verbs.Usagef("mc collie launch-bind requires --launch and --container-id")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.CollieLaunchBind(db, id, sessionID, *launch, *containerID)
		})

	case "runner-started":
		// Runner-private lifecycle receipt.
		sessionID, rest, err := positional("mc collie runner-started", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc collie runner-started")
		launch := fs.String("launch", "", "16-hex current launch id")
		containerID := fs.String("container-id", "", "64-hex Docker container id")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if *launch == "" || *containerID == "" {
			return nil, verbs.Usagef("mc collie runner-started requires --launch and --container-id")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.CollieRunnerStarted(db, id, sessionID, *launch, *containerID)
		})

	case "exit":
		// Resident-observed exit receipt.
		sessionID, rest, err := positional("mc collie exit", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc collie exit")
		launch := fs.String("launch", "", "16-hex current launch id")
		containerID := fs.String("container-id", "", "64-hex Docker container id")
		reason := fs.String("reason", "", "exit reason")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if *launch == "" || *containerID == "" || *reason == "" {
			return nil, verbs.Usagef("mc collie exit requires --launch, --container-id, and --reason")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.CollieExit(db, id, sessionID, *launch, *containerID, *reason)
		})
	}
	return nil, verbs.Usagef("unknown subverb: mc collie %s", args[0])
}

func cmdOutbox(args []string) (any, error) {
	if len(args) == 0 {
		return nil, verbs.Usagef("usage: mc outbox poll|ack|ack-rendered …")
	}
	switch args[0] {
	case "poll":
		fs := newFlags("mc outbox poll")
		after := fs.Int64("after", 0, "last outbox identifier of the previous page")
		surface := fs.String("surface", "", "delivery surface")
		limit := fs.Int("limit", 100, "maximum rows")
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		if *surface == "" {
			return nil, verbs.Usagef("mc outbox poll requires --surface")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		// Refusal precedes spine open.
		if err := verbs.RequireHostScope(id, "mc outbox poll"); err != nil {
			return nil, err
		}
		return withSpineRead(func(db *sql.DB) (any, error) {
			return verbs.OutboxPoll(db, id, *surface, *limit, *after)
		})

	case "ack-rendered":
		fs := newFlags("mc outbox ack-rendered")
		surface := fs.String("surface", "", "delivery surface")
		kind := fs.String("kind", "", "alert kind")
		idsJSON := fs.String("ids", "[]", "displayed record ids as a JSON array")
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		var recordIDs []int64
		if err := json.Unmarshal([]byte(*idsJSON), &recordIDs); err != nil {
			return nil, verbs.Usagef("record ids must be an array of integers")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc outbox ack-rendered"); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.OutboxAckRendered(db, id, *surface, *kind, recordIDs)
		})

	case "ack":
		rowID, rest, err := positionalID("mc outbox ack", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc outbox ack")
		surface := fs.String("surface", "", "delivery surface")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		if *surface == "" {
			return nil, verbs.Usagef("mc outbox ack requires --surface")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		// Refusal precedes spine open.
		if err := verbs.RequireHostScope(id, "mc outbox ack"); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.OutboxAck(db, id, rowID, *surface)
		})
	}
	return nil, verbs.Usagef("unknown subverb: mc outbox %s", args[0])
}

func cmdVerifier(args []string) (any, error) {
	if len(args) == 0 || args[0] != "verdict" {
		return nil, verbs.Usagef("usage: mc verifier verdict <task> --run <id> --outcome … --evidence … --sha …")
	}
	task, rest, err := positionalID("mc verifier verdict", args[1:])
	if err != nil {
		return nil, err
	}
	fs := newFlags("mc verifier verdict")
	run := fs.String("run", "", "fencing token")
	outcome := fs.String("outcome", "", "pass|correct")
	evidence := fs.String("evidence", "", "gate-ladder evidence path")
	sha := fs.String("sha", "", "the verified commit SHA")
	correction := fs.String("correction", "", "correction file path (CORRECT)")
	problem := fs.String("problem", "", "unresolved problem and failed criteria")
	explanationEvidence := fs.String("explanation-evidence", "", "evidence for the rejection")
	resolution := fs.String("resolution", "", "proposed correction that satisfies the failed criteria")
	if err := parse(fs, rest); err != nil {
		return nil, err
	}
	if *run == "" {
		return nil, verbs.Usagef("mc verifier verdict requires --run")
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	return withSpine(func(db *sql.DB) (any, error) {
		return verbs.VerifierVerdict(db, id, verbs.VerdictArgs{
			Task: task, Run: *run, Outcome: *outcome, Evidence: *evidence,
			SHA: *sha, Correction: *correction, Problem: *problem, ExplanationEvidence: *explanationEvidence, Resolution: *resolution,
		})
	})
}

func cmdRun(args []string) (any, error) {
	if len(args) > 0 && (args[0] == "customization" || args[0] == "customization-conflict") {
		return cmdRunCustomization(args)
	}
	if len(args) == 2 && args[0] == "launch-check" {
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "launch check"); err != nil {
			return nil, err
		}
		required, err := systemRequirements()
		if err != nil {
			return nil, err
		}
		return systemSpineCall("launch-check", map[string]any{"run": args[1], "requirements": required})
	}
	if len(args) > 0 && args[0] == "stopped" {
		runID, rest, err := positional("mc run stopped", args[1:])
		if err != nil {
			return nil, err
		}
		if len(rest) != 0 {
			return nil, verbs.Usagef("mc run stopped accepts one run identity")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		if err := verbs.RequireHostScope(id, "mc run stopped"); err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.RunStopped(db, id, runID) })
	}
	if len(args) > 0 && args[0] == "model-response" {
		if len(args) != 2 {
			return nil, verbs.Usagef("The model response requires one run identity.")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.RunModelResponse(db, id, args[1]) })
	}
	if len(args) > 0 && args[0] == "process-started" {
		if len(args) != 2 {
			return nil, verbs.Usagef("usage: mc run process-started <run_id>")
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) { return verbs.RunProcessStarted(db, id, args[1]) })
	}
	if len(args) > 0 && args[0] == "infra-failure" {
		runID, rest, err := positional("mc run infra-failure", args[1:])
		if err != nil {
			return nil, err
		}
		fs := newFlags("mc run infra-failure")
		proxyRequest := fs.String("proxy-request-id", "", "host provider request identity")
		responseStatus := fs.Int("http-status", 0, "provider response status")
		providerCode := fs.String("provider-error-code", "", "recognized provider code")
		networkError := fs.String("network-error", "", "connect|reset|timeout|tls|other for a request without a provider response")
		condition := fs.String("condition", "", "observed execution failure")
		source := fs.String("source", "agent", "agent|resident|runner")
		cause := fs.String("cause", "unknown", "unknown|credentials|quota|connection")
		evidence := fs.String("evidence", "", "evidence for the reported cause")
		action := fs.String("operator-action", "", "action supported by the evidence")
		reset := fs.String("quota-reset-at", "", "service-supplied quota reset time")
		if err := parse(fs, rest); err != nil {
			return nil, err
		}
		id, err := verbs.LoadIdentity()
		if err != nil {
			return nil, err
		}
		return withSpine(func(db *sql.DB) (any, error) {
			return verbs.RunInfrastructureFailure(db, id, runID, domain.InfrastructureFailureReport{ProxyRequestID: *proxyRequest, HTTPStatus: optionalResponseStatus(*responseStatus), ProviderErrorCode: *providerCode,
				NetworkError: *networkError, Condition: *condition, Source: *source, Cause: *cause, Evidence: *evidence,
				OperatorAction: *action, QuotaResetAt: *reset,
			})
		})
	}
	if len(args) >= 1 && args[0] == "list" {
		fs := newFlags("mc run list")
		task := fs.Int64("task", 0, "restrict to the runs of one task")
		project := fs.String("project", "", "restrict to the runs of one Project")
		if err := parse(fs, args[1:]); err != nil {
			return nil, err
		}
		if *task < 0 {
			return nil, verbs.Usagef("mc run list: bad task id %d", *task)
		}
		return withSpineRead(func(db *sql.DB) (any, error) {
			return verbs.RunList(db, verbs.RunSelection{Task: *task, Project: *project})
		})
	}
	if len(args) == 0 || args[0] != "register-session" {
		return nil, verbs.Usagef("usage: mc run register-session <run_id> --native-ref <ref> --file <name> | mc run list [--task <id>] [--project <id>]")
	}
	runID, rest, err := positional("mc run register-session", args[1:])
	if err != nil {
		return nil, err
	}
	fs := newFlags("mc run register-session")
	nativeRef := fs.String("native-ref", "", "harness native session handle")
	file := fs.String("file", "", "trace filename")
	if err := parse(fs, rest); err != nil {
		return nil, err
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return nil, err
	}
	return withSpine(func(db *sql.DB) (any, error) {
		return verbs.RegisterSession(db, id, runID, *nativeRef, *file)
	})
}

func optionalResponseStatus(status int) *int {
	if status == 0 {
		return nil
	}
	return &status
}
