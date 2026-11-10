package verbs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
)

// ArbiterVerdicts is the payload for mc arbiter decide.
type ArbiterVerdicts struct {
	Verdicts []struct {
		Task              int64   `json:"task"`
		Decision          string  `json:"decision"`
		Reason            string  `json:"reason"`
		DuplicateOf       *int64  `json:"duplicate_of,omitempty"`
		RejectionCategory *string `json:"rejection_category,omitempty"`
	} `json:"verdicts"`
}

func decodeStrictJSON(r io.Reader, dst any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value after the batch document")
		}
		return fmt.Errorf("trailing data after the batch document: %w", err)
	}
	return nil
}

// The Arbiter judges invalid criteria and overlap. The command checks each
// declaration and requires duplicate references to name work outside the pool.
func requireAllRejectException(ctx context.Context, q Q, payload ArbiterVerdicts, pool []int64, project string) error {
	inPool := make(map[int64]bool, len(pool))
	for _, p := range pool {
		inPool[p] = true
	}
	for _, v := range payload.Verdicts {
		if v.RejectionCategory != nil && *v.RejectionCategory == "invalid_criteria" {
			continue
		}
		if v.DuplicateOf == nil {
			return &DomainError{Code: domain.CodeZeroPromotion,
				Msg: fmt.Sprintf("zero-promotion batch rejected: the ready queue would be empty. "+
					"Each rejection requires a valid duplicate_of reference or rejection_category invalid_criteria. "+
					"Task %d supplies neither exception", v.Task)}
		}
		cited := *v.DuplicateOf
		if inPool[cited] {
			return &DomainError{Code: domain.CodeZeroPromotion,
				Msg: fmt.Sprintf("task %d cites duplicate_of %d, a member of its own pool: pool-internal overlap is "+
					"resolved by promoting the superset, so it cannot justify promoting nothing", v.Task, cited)}
		}
		var n int
		if err := q.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM tasks
			WHERE id = ? AND project = ?
			  AND ((archived = 0 AND status <> 'proposed')
			       OR (archived = 1 AND decision = 'approved'))`, cited, project).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return &DomainError{Code: domain.CodeZeroPromotion,
				Msg: fmt.Sprintf("task %d cites duplicate_of %d, which is not existing work of Project %s: a cited row must be a "+
					"row of the "+partActiveTasks+" part or a merged task of this Project", v.Task, cited, project)}
		}
	}
	return nil
}

// ArbiterDecide validates and applies the complete batch in one transaction.
func ArbiterDecide(db *sql.DB, id *RunIdentity, run string, batch io.Reader) (any, error) {
	// The run identity must identify the Arbiter role.
	if err := requireExactRole(id, "arbiter"); err != nil {
		return nil, err
	}
	if err := requireOwnRun(id, run); err != nil {
		return nil, err
	}
	var payload ArbiterVerdicts
	if err := decodeStrictJSON(batch, &payload); err != nil {
		return nil, Domainf("mc arbiter decide: bad batch payload: %v", err)
	}
	allReject := true
	for _, v := range payload.Verdicts {
		if v.RejectionCategory != nil {
			if v.Decision != "reject" {
				return nil, Domainf("task %d: rejection_category requires a reject decision", v.Task)
			}
			if *v.RejectionCategory != "invalid_criteria" {
				return nil, Domainf("task %d: unknown rejection_category %q", v.Task, *v.RejectionCategory)
			}
			if v.DuplicateOf != nil {
				return nil, Domainf("task %d: rejection_category and duplicate_of are mutually exclusive", v.Task)
			}
		}
		switch v.Decision {
		case "promote":
			allReject = false
			if v.DuplicateOf != nil {
				return nil, Domainf("task %d: duplicate_of belongs to a reject, not a promote", v.Task)
			}
		case "reject":
			if strings.TrimSpace(v.Reason) == "" {
				return nil, &DomainError{Code: domain.CodeReasonRequired,
					Msg: fmt.Sprintf("reject of task %d requires a reason", v.Task)}
			}
			if v.DuplicateOf != nil && *v.DuplicateOf <= 0 {
				return nil, Domainf("task %d: duplicate_of must name a task id, got %d", v.Task, *v.DuplicateOf)
			}
		default:
			return nil, Domainf("unknown decision %q (promote|reject)", v.Decision)
		}
	}

	promoted, rejected := []int64{}, []int64{}
	err := inTx(db, func(ctx context.Context, q Q) error {
		if _, err := fenceRun(ctx, q, run); err != nil {
			return err
		}
		project, err := runProject(ctx, q, run)
		if err != nil {
			return err
		}
		// Coverage: the batch must cover exactly the run's snapshotted pool —
		// no more, no fewer; proposals that arrived after the
		// snapshot wait for the next batch.
		var poolJSON sql.NullString
		if err := q.QueryRowContext(ctx,
			`SELECT pool_snapshot FROM runs WHERE id = ?`, run).Scan(&poolJSON); err != nil {
			return err
		}
		var pool []int64
		if poolJSON.Valid {
			if err := json.Unmarshal([]byte(poolJSON.String), &pool); err != nil {
				return fmt.Errorf("parse pool_snapshot for run %s: %w", run, err)
			}
		}
		got := make([]int64, 0, len(payload.Verdicts))
		for _, v := range payload.Verdicts {
			got = append(got, v.Task)
		}
		if !sameIDSet(pool, got) {
			return &DomainError{Code: domain.CodePoolMismatch,
				Msg: fmt.Sprintf("batch must cover exactly the run's snapshotted pool %v, got %v", pool, got)}
		}

		// Ready work in another Project does not satisfy this Project's guard.
		pendingPool := false
		for _, verdict := range payload.Verdicts {
			pending, err := domain.RequirementsRevisionPending(ctx, q, verdict.Task)
			if err != nil {
				return err
			}
			pendingPool = pendingPool || pending
		}
		if allReject && len(payload.Verdicts) > 0 && !pendingPool {
			args := []any{project}
			ph := make([]string, 0, len(pool))
			for _, p := range pool {
				args = append(args, p)
				ph = append(ph, "?")
			}
			query := `SELECT COUNT(*) FROM tasks WHERE archived = 0 AND blocked = 0 AND stalled = 0 AND stage_rank > 0 AND project = ?`
			if len(ph) > 0 {
				query += ` AND id NOT IN (` + strings.Join(ph, ",") + `)`
			}
			var ready int
			if err := q.QueryRowContext(ctx, query, args...).Scan(&ready); err != nil {
				return err
			}
			if ready == 0 {
				if err := requireAllRejectException(ctx, q, payload, pool, project); err != nil {
					return err
				}
			}
		}

		for _, v := range payload.Verdicts {
			pending, err := domain.RequirementsRevisionPending(ctx, q, v.Task)
			if err != nil {
				return err
			}
			if pending {
				body, err := json.Marshal(v)
				if err != nil {
					return err
				}
				if _, err := q.ExecContext(ctx, `INSERT INTO activity(actor,kind,subject,detail) VALUES('arbiter','proposal.superseded',?,?)`, v.Task, string(body)); err != nil {
					return err
				}
				continue
			}
			switch v.Decision {
			case "promote":
				if err := domain.Promote(ctx, q, v.Task); err != nil {
					return err
				}
				promoted = append(promoted, v.Task)
			case "reject":
				if err := domain.RejectProposal(ctx, q, v.Task, v.Reason); err != nil {
					return err
				}
				rejected = append(rejected, v.Task)
			}
		}
		if err := endRun(ctx, q, run, "completed"); err != nil {
			return err
		}
		return releaseLease(ctx, q, run)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"promoted": promoted, "rejected": rejected}, nil
}

func sameIDSet(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	as := append([]int64(nil), a...)
	bs := append([]int64(nil), b...)
	sort.Slice(as, func(i, j int) bool { return as[i] < as[j] })
	sort.Slice(bs, func(i, j int) bool { return bs[i] < bs[j] })
	for i := range as {
		if as[i] != bs[i] {
			return false
		}
	}
	return true
}

// StrategistProposals is mc strategist propose's stdin payload.
// An empty array is legal: the fake Strategist's liveness terminal. A
// proposal's `project` is optional and, when present, must name the run's
// Project: the run inserts into the pool of that Project only.
type StrategistProposals struct {
	Proposals []struct {
		Project     string `json:"project"`
		Scope       string `json:"scope"`
		Title       string `json:"title"`
		Description string `json:"description"`
	} `json:"proposals"`
}

// StrategistPropose inserts all proposals in one transaction under the
// run's subjectless lease and releases it — the run's terminal action. Every
// proposal is filed into the pool of the run's Project: an omitted `project`
// defaults to it and a different one is refused. The insert rides the domain
// birth helper.
func StrategistPropose(db *sql.DB, id *RunIdentity, run string, batch io.Reader) (any, error) {
	if err := requireExactRole(id, string(dispatch.RoleStrategist)); err != nil {
		return nil, err
	}
	if err := requireOwnRun(id, run); err != nil {
		return nil, err
	}
	var payload StrategistProposals
	if err := decodeStrictJSON(batch, &payload); err != nil {
		return nil, Domainf("mc strategist propose: bad batch payload: %v", err)
	}
	if len(payload.Proposals) > 2 {
		return nil, Domainf("mc strategist propose accepts at most two tasks")
	}
	for i, p := range payload.Proposals {
		if p.Title == "" {
			return nil, Domainf("proposal %d: title is required", i)
		}
		if p.Scope != "" && p.Scope != "task" {
			return nil, Domainf("proposal %d: scope must be task", i)
		}
	}

	ids := []int64{}
	err := inTx(db, func(ctx context.Context, q Q) error {
		subject, err := fenceRun(ctx, q, run)
		if err != nil {
			return err
		}
		if subject != nil {
			return Domainf("Strategist requires a subjectless lease; run %s carries task %d", run, *subject)
		}
		project, err := runProject(ctx, q, run)
		if err != nil {
			return err
		}
		for i, p := range payload.Proposals {
			if p.Project != "" && p.Project != project {
				return Domainf("proposal %d names Project %q; run %s belongs to Project %q and inserts into its pool only", i, p.Project, run, project)
			}
			// origin='autonomous': the schema's agent-provenance value. The
			// substrate CHECK pins the vocabulary to user|autonomous.
			tid, err := domain.BirthProposal(ctx, q, domain.ProposalArgs{
				Title:       p.Title,
				Description: p.Description,
				Scope:       p.Scope,
				Origin:      "autonomous",
				Project:     project,
			})
			if err != nil {
				return err
			}
			ids = append(ids, tid)
		}
		if err := endRun(ctx, q, run, "completed"); err != nil {
			return err
		}
		return releaseLease(ctx, q, run)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"task_ids": ids}, nil
}

// VerdictArgs carries mc verifier verdict's flags.
type VerdictArgs struct {
	Task                int64
	Run                 string
	Outcome             string // pass | correct
	Evidence            string
	SHA                 string
	Correction          string // required for correct
	Problem             string
	ExplanationEvidence string
	Resolution          string
	// StoreWorktree is the fixed container path of the isolated repository.
	// The CLI supplies it; the working directory of the agent is never used.
	StoreWorktree string
}

// VerifierVerdict records the evidence and applies the Verifier result.
func VerifierVerdict(db *sql.DB, id *RunIdentity, a VerdictArgs) (any, error) {
	if err := requireRole(id, "verifier"); err != nil {
		return nil, err
	}
	if err := requireOwnRun(id, a.Run); err != nil {
		return nil, err
	}
	switch a.Outcome {
	case "pass", "correct":
	default:
		return nil, Usagef("mc verifier verdict requires --outcome pass|correct")
	}
	if a.Evidence == "" {
		return nil, Usagef("mc verifier verdict requires --evidence (every gate records evidence)")
	}
	if a.Outcome == "pass" && a.SHA == "" {
		return nil, Usagef("mc verifier verdict requires --sha (the pass names the exact submitted commit)")
	}
	if a.Outcome == "correct" && a.SHA != "" {
		return nil, Usagef("--sha is forbidden for CORRECT; that outcome names no verified commit")
	}
	if a.Outcome != "correct" && a.Correction != "" {
		return nil, Usagef("--correction is legal only with --outcome correct")
	}
	var expected string
	if err := db.QueryRow(`SELECT input_commit FROM repository_runs WHERE run_id=? AND task_id=?`, a.Run, a.Task).Scan(&expected); err != nil {
		return nil, err
	}
	root := a.StoreWorktree
	if root == "" {
		root = filepath.Join(completionStoreRoot(), "source")
	}
	git := repository.Git{Root: root}
	if head, err := git.Commit(context.Background(), "HEAD"); err != nil || head != expected {
		return nil, Domainf("the Verifier must assess its exact assigned commit")
	}
	if err := git.Clean(context.Background()); err != nil {
		return nil, err
	}
	// The two recorded paths are checked LAST of the pre-transaction fences,
	// after provenance. Order is deliberate: the fences above decide whether
	// this verdict may exist at all — whether the judged tree drifted, whether
	// the SHA is the one that was actually reviewed — and telling an agent to
	// fix its evidence file while its verdict is invalid regardless is the
	// wrong first answer. Everything here still runs before the transaction, so
	// a refusal on either path moves nothing.
	// The rule is the same as for the other terminals: a path a terminal
	// records is OPENED by the terminal. This one matters more than the two
	// others, because the correction file is not a record of finished
	// work — it is the ONLY channel the bounded rally has. The
	// verdict stores a pointer, never prose, and the next Worker's brief hands
	// that pointer straight to the agent whose directive calls it binding
	// input. So an unresolvable path does not degrade the round; it silently
	// spends one of three, with the Worker guessing what the objection was.
	//
	// The artifact plane is the check because it is the correction's one
	// durable home: the shared store is reset and cleaned by the next run's
	// preamble, and everything else the Verifier can write is
	// container-local. It is bound RW for the Worker too, so a file that
	// passes here is a file the next round can actually open.
	if a.Outcome == "correct" {
		if err := requireDurableRunArtifact(id, durableArtifactCheck{
			Flag:    "mc verifier verdict --outcome correct --correction",
			Path:    a.Correction,
			Max:     maxCorrectionBytes,
			Why:     "so the next Worker would receive a pointer to nothing and spend a rally round guessing the objection",
			Empty:   "an empty correction tells the next Worker nothing, and the rally budget is only three rounds",
			Gone:    "the correction was never written, so the rally would burn a round on an objection the Worker cannot read",
			Bound:   "correction",
			Leaf:    "corrections",
			Subject: a.Task,
		}, id.ArtifactPlanes()); err != nil {
			return nil, err
		}
	}
	// The same rule at the verdict's OTHER recorded path, and it is owed on all
	// three arms rather than one. Evidence is what makes a verdict checkable:
	// the verdict stores a pointer, the Packager's brief carries that pointer
	// as `latest_verdict.evidence_path`, and every criterion must bind a
	// concrete evidence artifact the packet then cites. A path that dies with
	// the Verifier's container leaves the packet asserting a verdict whose
	// grounds nobody — not the Packager, not the operator — can open.
	//
	// A PASS is the arm that matters most here, which is why this is not folded
	// into the CORRECT branch below: a correction at least gets a second round,
	// while a pass whose evidence is unreadable ships straight to the operator.
	if err := requireDurableRunArtifact(id, durableArtifactCheck{
		Flag:    "mc verifier verdict --evidence",
		Path:    a.Evidence,
		Max:     maxEvidenceBytes,
		Why:     "so the packet would cite a gate ladder nobody can open, and the criterion-to-evidence mapping would exist only in a session that is already gone",
		Empty:   "an empty file records no gate results, and a verdict without evidence is an opinion",
		Gone:    "the evidence was never written, so the packet would be born citing nothing",
		Bound:   "evidence",
		Leaf:    "verification",
		Subject: a.Task,
	}, id.ArtifactPlanes()); err != nil {
		return nil, err
	}

	originalEvidence, originalCorrection := a.Evidence, a.Correction
	var err error
	a.Evidence, err = snapshotRunArtifact(id, a.Evidence, maxEvidenceBytes)
	if err != nil {
		return nil, err
	}
	if a.Correction != "" {
		a.Correction, err = snapshotRunArtifact(id, a.Correction, maxCorrectionBytes)
		if err != nil {
			return nil, err
		}
	}

	var res domain.VerdictResult
	err = inTx(db, func(ctx context.Context, q Q) error {
		if err := recordTaskArtifacts(ctx, q, a.Task, originalEvidence, originalCorrection, a.Evidence, a.Correction); err != nil {
			return err
		}
		subject, err := fenceRun(ctx, q, a.Run)
		if err != nil {
			return err
		}
		if subject == nil || *subject != a.Task {
			return Domainf("task %d is not the live lease's subject", a.Task)
		}
		res, err = domain.ApplyVerdict(ctx, q, domain.VerdictArgs{
			TaskID:         a.Task,
			RunID:          a.Run,
			Outcome:        a.Outcome,
			EvidencePath:   a.Evidence,
			VerifiedSHA:    a.SHA,
			CorrectionPath: a.Correction, Problem: a.Problem, Evidence: a.ExplanationEvidence, Resolution: a.Resolution,
		})
		if err != nil {
			return err
		}
		if err := endRun(ctx, q, a.Run, "completed"); err != nil {
			return err
		}
		return releaseLease(ctx, q, a.Run)
	})
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"task_id":          a.Task,
		"outcome":          a.Outcome,
		"status":           res.Status,
		"correction_count": res.CorrectionCount,
	}
	if res.Status == "verified" {
		out["verified_sha"] = a.SHA
	}
	return out, nil
}
