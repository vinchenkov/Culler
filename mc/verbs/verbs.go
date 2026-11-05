// Package verbs implements the mc verb surface as testable functions over
// an open spine connection. cmd/mc is the thin CLI layer over this package.
//
// Common contract: each verb returns a single JSON-marshalable
// value (the stdout object); rejections are typed — DomainError is exit 1
// (fencing mismatch, validation, illegal transition; substrate aborts
// surface here), UsageError is exit 2 (usage/environment). Every mutation
// runs under BEGIN IMMEDIATE on one connection (substrate.Open); timestamps
// are written with datetime('now') inside the transaction — host processes
// never pass timestamps in. The spine clock is the only clock.
package verbs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"modernc.org/sqlite"

	"mc/domain"
)

// DomainError is a domain rejection: exit 1. It is mc/domain's coded error,
// re-exported so existing signatures keep their type.
type DomainError = domain.DomainError

// Domainf builds an uncoded DomainError (CLI-plane validation); domain-layer
// rejections carry their stable Code slug.
func Domainf(format string, a ...any) error {
	return &DomainError{Msg: fmt.Sprintf(format, a...)}
}

// UsageError is a usage/environment rejection: exit 2.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Usagef builds a UsageError.
func Usagef(format string, a ...any) error {
	return &UsageError{Msg: fmt.Sprintf(format, a...)}
}

// SpineInfrastructureError is implemented by a closed lock-domain transport
// whose failure means the storage crossing failed, not that the requested
// state transition was rejected on its merits.
type SpineInfrastructureError interface {
	error
	SpineInfrastructureFailure()
}

// Q is the query surface verbs run against inside a transaction — moved to
// mc/domain and re-exported.
type Q = domain.Q

// inTx runs fn under BEGIN IMMEDIATE on a single connection. Any error from
// fn rolls the transaction back and is returned;
// raw SQL errors (the substrate's trigger/CHECK aborts among them) are
// wrapped as DomainError unless already typed.
func inTx(db *sql.DB, fn func(ctx context.Context, q Q) error) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return Usagef("acquire spine connection: %v", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return Usagef("begin immediate: %v", err)
	}
	if err := fn(ctx, conn); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return classify(err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = conn.ExecContext(ctx, "ROLLBACK")
		return classify(err)
	}
	return nil
}

// classify maps untyped errors (substrate aborts, constraint failures) to
// DomainError; typed errors pass through.
//
// A driver-level fault is NOT a domain rejection and must not be reported as
// one. Reporting SQLite's own errors under `domain-rejection` tells an agent —
// and an operator reading a log — that the spine refused the request on the
// merits, when in fact the storage layer failed. That laundering cost a real
// diagnosis cycle once: SQLite's `locking protocol (15)`
// (SQLITE_PROTOCOL, raised by a wal-index handshake failure) was read as a
// Culler guard and chased as a design blocker. These faults keep the
// driver's message but carry their own code so they are legible as
// infrastructure.
func classify(err error) error {
	switch err.(type) {
	case *DomainError, *UsageError:
		return err
	}
	var infrastructure SpineInfrastructureError
	if errors.As(err, &infrastructure) {
		return Usagef("%v", err)
	}
	var serr *sqlite.Error
	if errors.As(err, &serr) {
		return &DomainError{Code: domain.CodeSpineFault, Msg: err.Error()}
	}
	return &DomainError{Msg: err.Error()}
}

// newRunID allocates a run id: 16 hex chars, strict container-name-safe
// charset (container name mc-<project>-<run_id>).
func newRunID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", Usagef("allocate run id: %v", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// spineTime is the datetime('now') storage format.
const spineTime = "2006-01-02 15:04:05"

func parseSpineTime(s string) (time.Time, error) {
	t, err := time.Parse(spineTime, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse spine timestamp %q: %w", s, err)
	}
	return t, nil
}

// baseRole maps a wire role identifier to the role in lease and run records.
func baseRole(role string) string {
	if i := strings.IndexByte(role, '('); i >= 0 {
		return role[:i]
	}
	return role
}

// RunIdentity is the read-only launch envelope identity:
// tier and role are read only from run.json, never from arguments.
type RunIdentity struct {
	Launch         string                `json:"launch"`
	RuntimeBinding PrivateRuntimeBinding `json:"runtime_binding"`
	RunID          string                `json:"run_id"`
	Tier           string                `json:"tier"`
	Role           string                `json:"role"`
	PoolIDs        []int                 `json:"pool_ids"`
	VerbAllowlist  []string              `json:"verb_allowlist"`
	Mounts         RunMounts             `json:"mounts"`
}

// RunMounts is the launch envelope's own account of what the committed mount
// plan made writable. The resident derives it from the RW rows of that plan
// beside the very `-v` set it builds, so it is the run's authoritative answer
// to "which destinations here outlive this container".
//
// It rides the same read-only run.json as tier and role, so it carries the
// same trust: an agent cannot rewrite its own confinement, and therefore
// cannot widen this list.
type RunMounts struct {
	AllowWrite []string `json:"allow_write"`
}

// ArtifactPlanes returns the run's writable artifact-plane destinations —
// the subset of the envelope's writable set that lies beneath the class root.
//
// This is what makes "durable" checkable rather than assumed. The class root
// itself (`/mc/artifacts`) is ordinary image rootfs: a role can create files
// and directories directly in it, they are writable, and they die with the
// `--rm` container. Only the bound `<root>/<target>` planes are real host
// binds. Membership here is the difference, and it cannot be inferred from the
// path's shape — a fabricated `<root>/<made-up-target>/x` looks identical.
func (id *RunIdentity) ArtifactPlanes() []string {
	if id == nil {
		return nil
	}
	root := ArtifactsMountRoot()
	planes := make([]string, 0, len(id.Mounts.AllowWrite))
	for _, dest := range id.Mounts.AllowWrite {
		if strings.HasPrefix(dest, root+string(filepath.Separator)) {
			planes = append(planes, dest)
		}
	}
	return planes
}

// RequireHostScope fences host-effect and resident-only verbs before they
// open or mutate the spine. Scope is derived only from run.json
// presence; a flag can never counterfeit it.
func RequireHostScope(id *RunIdentity, verb string) error {
	if id != nil {
		return Domainf("%s is host scope only; run.json callers are refused", verb)
	}
	return nil
}

// RequireOperatorVerb admits the host and a Homie only when the verb was
// frozen into that session's allowlist. Pipeline identities are categorically
// denied operator provenance.
func RequireOperatorVerb(id *RunIdentity, verb string) error {
	if id == nil {
		return nil
	}
	if id.Tier == "homie" {
		for _, allowed := range id.VerbAllowlist {
			if allowed == verb {
				return nil
			}
		}
		return Domainf("Homie session is not allowed operator verb %s", verb)
	}
	return Domainf("%s is an operator verb, denied to pipeline runs", verb)
}

// RunJSONPath resolves the fixed in-container identity mount. Direct host and
// CLI-test invocations retain MC_RUN_JSON as a test seam; a privileged image
// invocation structurally ignores it and consumes only /mc/run.json.
func RunJSONPath() string {
	return runJSONPathForCredentials(runtime.GOOS, os.Getuid(), os.Geteuid(), os.Getenv("MC_RUN_JSON"))
}

// runJSONPathForCredentials makes the production setuid boundary explicit
// and independently testable. A privileged image invocation must consume the
// immutable nested mount; an agent-controlled environment can never redirect
// or suppress its identity. Direct host/test invocations retain the override
// seam because their real and effective uids are equal.
func runJSONPathForCredentials(goos string, ruid, euid int, override string) string {
	if goos != "windows" && ruid != euid {
		return "/mc/run.json"
	}
	if override != "" {
		return override
	}
	return "/mc/run.json"
}

// ArtifactsMountRoot resolves the container's durable artifact plane
// (`/mc/artifacts/<artifact-target>/<suffix>`, backed by the profile's
// `artifact_roots`). It is the only place a pipeline run can leave bytes that
// outlive its `--rm` container without going through `mc`, which is why the
// Packager terminal resolves `--outputs` beneath it.
//
// The plane is on `/mc`, not under `/workspace`: a task-scoped role binds
// `/workspace` read-only, so a nested mountpoint there cannot be created and
// the container never starts (mountplan.go). The HOST source is unchanged —
// the bytes still land in the Project's own `.mc-artifacts`, openable beside
// the code they review.
//
// Same credential guard as RunJSONPath, for the same reason.
func ArtifactsMountRoot() string {
	return fixedMountRoot("/mc/artifacts", runtime.GOOS, os.Getuid(), os.Geteuid(),
		os.Getenv("MC_ARTIFACTS_ROOT"))
}

// completionStoreRoot resolves the root of the isolated repository of the
// run: the fixed /workspace bind of an agent container, with the source at
// /workspace/source. Only a host or a CLI test invocation can substitute it.
func completionStoreRoot() string {
	return fixedMountRoot("/workspace", runtime.GOOS, os.Getuid(), os.Geteuid(),
		os.Getenv("MC_TASK_STORE_ROOT"))
}

// fixedMountRoot is the shared shape of every "this container path is fixed"
// resolution: a privileged image invocation consumes the mount and nothing
// else, so an agent that exports the override at a directory it controls
// cannot redirect the proof. Only a host or CLI-test invocation, where real
// and effective uid are equal, may substitute one.
func fixedMountRoot(fixed, goos string, ruid, euid int, override string) string {
	if goos != "windows" && ruid != euid {
		return fixed
	}
	if override != "" {
		return override
	}
	return fixed
}

// LoadIdentity reads run.json if present. (nil, nil) = host scope.
func LoadIdentity() (*RunIdentity, error) {
	b, err := os.ReadFile(RunJSONPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // host scope
		}
		return nil, Usagef("read run.json: %v", err)
	}
	var id RunIdentity
	if err := json.Unmarshal(b, &id); err != nil {
		return nil, Usagef("parse run.json: %v", err)
	}
	return &id, nil
}

// requirePipeline checks identity presence and tier, for the
// verbs whose role expectation is subject-dependent or role-neutral.
func requirePipeline(id *RunIdentity) error {
	if id == nil {
		return Domainf("this verb requires a pipeline run identity (run.json); host scope is refused")
	}
	if id.Tier != "pipeline" {
		return Domainf("this verb is pipeline-tier only; run.json tier is %q", id.Tier)
	}
	return nil
}

// roleMismatch is the coded refusal for a run.json role that does not match
// the verb.
func roleMismatch(id *RunIdentity, want string) error {
	return &DomainError{Code: domain.CodeRoleMismatch,
		Msg: fmt.Sprintf("role mismatch: run.json role is %q, verb requires %q", id.Role, want)}
}

// requireRole checks the pipeline-role scope: identity present,
// tier pipeline, and run.json role (base-role) matching want.
func requireRole(id *RunIdentity, want string) error {
	if err := requirePipeline(id); err != nil {
		return err
	}
	if got := baseRole(id.Role); got != want {
		return roleMismatch(id, want)
	}
	return nil
}

// requireExactRole checks the exact wire role identifier in run.json.
func requireExactRole(id *RunIdentity, want string) error {
	if err := requirePipeline(id); err != nil {
		return err
	}
	if id.Role != want {
		return roleMismatch(id, want)
	}
	return nil
}

// requireOwnRun binds a role terminal's caller-supplied fencing token to the
// immutable identity in run.json before the token is checked against the live
// lease. Lease fencing alone is insufficient: an old
// same-role container must never act as a newer holder merely by supplying
// that holder's run id.
func requireOwnRun(id *RunIdentity, runID string) error {
	if err := requirePipeline(id); err != nil {
		return err
	}
	if id.RunID == "" || id.RunID != runID {
		return &DomainError{Code: domain.CodeStaleRun,
			Msg: fmt.Sprintf("stale run / caller run mismatch: run.json identifies %q, --run supplies %q", id.RunID, runID)}
	}
	return nil
}

// fenceRun verifies the --run fencing token against the live lease — the
// domain's lease.Fence.
func fenceRun(ctx context.Context, q Q, runID string) (*int64, error) {
	return domain.Fence(ctx, q, runID)
}

// runProject returns the Project of a lease run: runs.project records it at
// claim. Called after fenceRun, so the run is known to hold its lease.
func runProject(ctx context.Context, q Q, runID string) (string, error) {
	var project sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT project FROM runs WHERE id = ?`, runID).Scan(&project); err != nil {
		return "", err
	}
	if !project.Valid || project.String == "" {
		return "", Domainf("run %s is bound to no Project", runID)
	}
	return project.String, nil
}

// releaseLease ends the execution reservation. Stage transitions reset the task allowance.
func releaseLease(ctx context.Context, q Q, runID string) error {
	return domain.Release(ctx, q, runID)
}

// endRun is the runs half of every role terminal: stamp ended_at/outcome.
// The run of a wiki update ends with its wiki update, through domain.EndWikiRun.
func endRun(ctx context.Context, q Q, runID, outcome string) error {
	var wikiUpdate bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_updates WHERE run_id=?)`, runID).Scan(&wikiUpdate); err != nil {
		return err
	}
	if wikiUpdate {
		return errors.New("a wiki update run ends through domain.EndWikiRun")
	}
	result, err := q.ExecContext(ctx,
		`UPDATE runs SET ended_at = datetime('now'), outcome = ? WHERE id = ? AND ended_at IS NULL AND outcome IS NULL`,
		outcome, runID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.Errf(domain.CodeStaleRun, "run %q already has a final outcome", runID)
	}
	if outcome == "completed" {
		var checkpoint bool
		if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM version_submissions WHERE run_id=? AND kind='checkpoint')`, runID).Scan(&checkpoint); err != nil {
			return err
		}
		project, err := runProject(ctx, q, runID)
		if err != nil {
			return err
		}
		if !checkpoint {
			if err := domain.ResetProjectInfrastructure(ctx, q, project, "mc", runID); err != nil {
				return err
			}
		}
	}
	// A role terminal shows that the run reached the model.
	return domain.ClearNetworkRetry(ctx, q, runID)
}
