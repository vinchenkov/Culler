package verbs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"mc/dispatch"
	"mc/domain"
	"mc/repository"
	"mc/substrate"
)

const (
	spawnBriefSchema = "mc.spawn-brief.v23"
	spawnBriefHeader = "Culler immutable run brief"
)

// The part headings. A role directive and a skill name a part by its heading.
const (
	partCustomization        = "Project role customization"
	partRun                  = "Run"
	partTask                 = "Task"
	partRevision             = "Revision request"
	partRequirementsRevision = "Requirements revision"
	partMergeCandidate       = "Merge candidate"
	partCorrection           = "Correction from the Verifier"
	partLastReport           = "Last completion report"
	partLatestVerdict        = "Latest verdict"
	partGitState             = "Git state"
	partPreviousPacket       = "Previous packet"
	partMergedTasks          = "Merged tasks"
	partPool                 = "Proposed pool"
	partActiveTasks          = "Active tasks"
	partRejected             = "Rejected work"
	partCancelled            = "Cancelled work"
	partProposalDecisions    = "Proposal decisions"
	partProject              = "Project"
	partVision               = "Vision"
)

// runBrief is the content of one run brief, selected for the role of the run.
// renderRunBrief writes the text that the agent and the operator read.
type runBrief struct {
	Role      dispatch.Role
	RunID     string
	Directive string
	Parts     []briefPart
}

// briefPart is one block of a run brief with one purpose. The heading and the
// explanation come before the data, so a reader who sees Culler for the first
// time knows what the data is and what to do with it.
type briefPart struct {
	Heading     string
	Explanation string
	Facts       []briefFact
	// Text is prose from outside the product, such as a task description. The
	// tag marks where that prose starts and ends.
	TextTag string
	Text    string
	// Rows is structured data, rendered as one JSON block.
	Rows any
}

type briefFact struct {
	Label string
	Value string
}

func (b runBrief) part(heading string) *briefPart {
	for i := range b.Parts {
		if b.Parts[i].Heading == heading {
			return &b.Parts[i]
		}
	}
	return nil
}

func (p *briefPart) fact(label string) string {
	if p == nil {
		return ""
	}
	for _, f := range p.Facts {
		if f.Label == label {
			return f.Value
		}
	}
	return ""
}

// briefInputs are the facts that dispatch resolved outside the spine snapshot.
type briefInputs struct {
	Vision *ProjectVisionCheck
	// Repository is set when the run receives the isolated repository of its task.
	Repository *substrate.DispatchRepository
}

// briefTaskRecord is one tasks row as the brief build reads it.
type briefTaskRecord struct {
	ID                  int64
	Title               string
	Description         string
	Scope               string
	Priority            int
	Status              string
	CorrectionCount     int
	ReturnedKind        string
	OperatorReason      string
	Blocked             bool
	BlockedReason       string
	RevisionNotes       string
	Decision            string
	Archived            bool
	RecordVersion       int64
	RequirementsVersion int64
}

// briefBoardRow is one task of the board, shown to a role that judges or plans
// against the board. A subject never carries board state: dispatch selects only
// an open, unblocked subject, so that state holds no information there.
type briefBoardRow struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Scope         string `json:"scope"`
	Priority      int    `json:"priority"`
	Status        string `json:"status"`
	Blocked       bool   `json:"blocked,omitempty"`
	BlockedReason string `json:"blocked_reason,omitempty"`
	Decision      string `json:"decision,omitempty"`
	Archived      bool   `json:"archived,omitempty"`
}

// briefActiveTitle is the Strategist's view of an active task. It holds a
// title and no description: a description pulls a generator toward small
// variations of active work.
type briefActiveTitle struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type briefHistoricalTask struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Decision string `json:"decision"`
	Reason   string `json:"reason,omitempty"`
}

type briefVerdict struct {
	RequirementsVersion int64
	Outcome             string
	EvidencePath        string
	CorrectionPath      string
}

// briefDelivery is the part of a delivery record that an agent can use. The
// merge state, the report record, and the cleanup record stay with the
// dispatcher and the dashboard.
type briefDelivery struct {
	BaseCommit        string
	InputCommit       string
	SubmittedCommit   string
	VersionID         sql.NullInt64
	DestinationBranch string
	CorrectionReason  string
	CheckpointVersion sql.NullInt64
}

func isRole(role dispatch.Role, roles ...dispatch.Role) bool {
	for _, r := range roles {
		if role == r {
			return true
		}
	}
	return false
}

// buildSpawnBrief writes the immutable opening input of a run from the same
// BEGIN IMMEDIATE snapshot that claims the lease. The resident copies the text
// into run.json unchanged and never reads the spine, so the brief cannot drift
// between the decision and the launch.
func buildSpawnBrief(ctx context.Context, q Q, sp *dispatch.Spawn, runID string, vision ...*ProjectVisionCheck) (string, error) {
	in := briefInputs{}
	if len(vision) > 0 {
		in.Vision = vision[0]
	}
	brief, err := buildRunBrief(ctx, q, sp, runID, in)
	if err != nil {
		return "", err
	}
	return renderRunBrief(brief)
}

func buildRunBrief(ctx context.Context, q Q, sp *dispatch.Spawn, runID string, in briefInputs) (runBrief, error) {
	if runID == "" {
		return runBrief{}, Domainf("spawn brief requires the claimed run id: each terminal command needs it as --run")
	}
	custom, err := customizationForRun(ctx, q, sp, runID)
	if err != nil {
		return runBrief{}, err
	}
	if sp.Role == dispatch.RoleScribe {
		return buildScribeBrief(ctx, q, sp, runID, custom, in)
	}
	b := runBrief{Role: sp.Role, RunID: runID, Directive: custom.StandardDirective}
	b.Parts = append(b.Parts,
		customizationPart(sp, custom, "A contract conflict ends the run with a customization conflict."),
		runPart(runID), credentialPart())
	environment, err := environmentStateTx(ctx, q, sp.Project)
	if err != nil {
		return runBrief{}, err
	}
	if in.Repository != nil {
		revision, image, candidateErr := environmentCandidateTx(ctx, q, sp.Project, in.Repository.InputCommit)
		err = candidateErr
		if err != nil && err != sql.ErrNoRows {
			return runBrief{}, err
		}
		if err == nil {
			environment.SelectedRevisionID, environment.SelectedImageDigest = revision, image
			candidate, loadErr := environmentRevisionTx(ctx, q, revision)
			if loadErr != nil {
				return runBrief{}, loadErr
			}
			environment.SelectedBaseDigest = candidate.Configuration.BaseDigest
		}
	}
	if environment.SelectedRevisionID != "" {
		b.Parts = append(b.Parts, briefPart{Heading: "Project environment", Explanation: "Culler supplies the selected image and persistent Project home. A task run receives a fresh source workspace and dependency preparation before the agent starts.", Facts: []briefFact{{Label: "Configuration revision", Value: environment.SelectedRevisionID}, {Label: "Project image", Value: environment.SelectedImageDigest}, {Label: "Selected base", Value: environment.SelectedBaseDigest}}})
	}

	var subject *briefTaskRecord
	if sp.SubjectID != nil {
		task, err := loadBriefTask(ctx, q, *sp.SubjectID)
		if err != nil {
			return runBrief{}, err
		}
		subject = &task
		if err := addSubjectParts(ctx, q, &b, sp, task, in); err != nil {
			return runBrief{}, err
		}
	}

	switch sp.Role {
	case dispatch.RoleArbiter:
		if err := addArbiterParts(ctx, q, &b, sp); err != nil {
			return runBrief{}, err
		}
	case dispatch.RoleStrategist:
		if err := addProposeParts(ctx, q, &b, sp); err != nil {
			return runBrief{}, err
		}
	}
	if subject == nil && (sp.Role == dispatch.RoleArbiter || sp.Role == dispatch.RoleStrategist) {
		if err := addHistoricalParts(ctx, q, &b, sp.Project); err != nil {
			return runBrief{}, err
		}
		rows, err := q.QueryContext(ctx, `SELECT id,question,resolution,resolved_at FROM proposal_decisions WHERE project=? AND resolved_at IS NOT NULL ORDER BY id`, sp.Project)
		if err != nil {
			return runBrief{}, err
		}
		decisions, err := rowsToMaps(rows)
		rows.Close()
		if err != nil {
			return runBrief{}, err
		}
		if len(decisions) > 0 {
			b.Parts = append(b.Parts, briefPart{Heading: partProposalDecisions, Explanation: "The operator resolved these Project proposal questions. Apply each relevant resolution to this assignment.", Rows: decisions})
		}
	}
	b.Parts = append(b.Parts, briefPart{Heading: "Failure controls", Explanation: "Execution problems and operator decisions use separate controls. A confirmed connection condition uses the connection controls.", Rows: []string{
		"The runner confirms connection recovery from complete model responses. Recovery preserves the task outcome and its retry allowance.",
		"Dispatch and launch failures use the Project infrastructure allowance before the agent process starts.",
		"Strategist proposal creation and Arbiter proposal review share that Project allowance throughout their runs.",
		"Assigned task failures use their task run failure allowance after the agent process starts.",
		"Each failure allowance permits three counted failures. An operator decision report consumes zero failure units.",
	}})
	addVisionPart(&b, in.Vision)
	if sp.Role == dispatch.RoleWorker {
		trimWorkerBrief(&b)
	}
	if sp.Role == dispatch.RoleStrategist {
		trimStrategistBrief(&b)
	}
	return b, nil
}

func trimStrategistBrief(b *runBrief) {
	parts := b.Parts[:0]
	for _, part := range b.Parts {
		switch part.Heading {
		case "Agent credentials", "Failure controls":
			continue
		case partCustomization:
			part.Explanation = "This part identifies the operator's custom instructions and their revision."
		case partRun:
			part.Explanation = "Use this identifier as `<run_id>` in command forms that require it."
		case "Project environment":
			part.Explanation = "These identifiers name the selected Project environment."
		case partActiveTasks:
			part.Explanation = "These proposals and active tasks are absent from the repository copy."
		case partRejected:
			part.Explanation = "These are recent rejected tasks."
		case partCancelled:
			part.Explanation = "These are recent cancelled tasks, with reason excerpts when available. Use each reason as context for the operator's preference."
		case partVision:
			part.Explanation = "This is the accepted `VISION.md` content."
		}
		parts = append(parts, part)
	}
	b.Parts = parts
}

// trimWorkerBrief keeps Worker procedures in the command skill.
func trimWorkerBrief(b *runBrief) {
	parts := b.Parts[:0]
	for _, part := range b.Parts {
		switch part.Heading {
		case "Agent credentials", "Correction allowances", "Failure controls":
			continue
		case partCustomization:
			part.Explanation = "This part identifies the operator's custom instructions and their revision."
		case partRun:
			part.Explanation = "Use this identifier as `<run_id>` in command forms that require it."
		case partTask:
			part.Explanation = "Use the Identifier as `<task>` in command forms."
			facts := part.Facts[:0]
			for _, fact := range part.Facts {
				if fact.Label != "Unsuccessful Verifier corrections" {
					facts = append(facts, fact)
				}
			}
			part.Facts = facts
		case "Project environment":
			part.Explanation = "Use these identifiers to assess dependency compatibility and prepare an environment proposal."
		case partGitState:
			part.Explanation = "Use these Git facts for the repository checks in the mc-worker skill."
		case partRequirementsRevision:
			part.Explanation = "This is the operator's reason for the requirements revision."
		case partRevision:
			part.Explanation = "This is the operator's request for this revision."
		case partMergeCandidate:
			part.Explanation = "These are the merge inputs and conflict paths."
		case partCorrection:
			part.Explanation = "These files contain the Verifier's objection and evidence."
		case "Operator allowance explanation":
			part.Explanation = "Use the operator's explanation as context for the unresolved condition."
		}
		parts = append(parts, part)
	}
	b.Parts = parts
}

// customizationPart holds the operator instructions of the role. The second
// sentence of its explanation states the result of a contract conflict.
func customizationPart(sp *dispatch.Spawn, custom RoleCustomization, conflict string) briefPart {
	return briefPart{
		Heading:     partCustomization,
		Explanation: "These operator instructions replace standard defaults within the Culler contract. " + conflict,
		Facts:       []briefFact{{"Project", sp.Project}, {"Role", string(sp.Role)}, {"Customization revision", strconv.FormatInt(custom.Revision, 10)}, {"Standard directive digest", custom.StandardDigest}},
		Rows:        map[string]any{"instructions": custom.Instructions},
	}
}

func credentialPart() briefPart {
	return briefPart{Heading: "Agent credentials", Explanation: "The agent profile selects the harness, model, and effort; the same role input applies to Codex, Claude, and OpenCode. Culler keeps provider credentials on the host and supplies this container with temporary proxy access."}
}

func runPart(runID string) briefPart {
	return briefPart{
		Heading:     partRun,
		Explanation: "This value identifies this run. It is the `<run_id>` value of each `mc` command.",
		Facts:       []briefFact{{"Run identifier", runID}},
	}
}

// buildScribeBrief writes the run brief of a wiki update run. The run has no
// task: its subject is the Project wiki at the start commit of the wiki update.
func buildScribeBrief(ctx context.Context, q Q, sp *dispatch.Spawn, runID string, custom RoleCustomization, in briefInputs) (runBrief, error) {
	b := runBrief{Role: sp.Role, RunID: runID, Directive: custom.StandardDirective}
	customization := customizationPart(sp, custom, "")
	customization.Explanation = "This part identifies the operator's custom instructions and their revision."
	b.Parts = append(b.Parts,
		customization,
		runPart(runID),
		briefPart{
			Heading:     partProject,
			Explanation: "The assigned repository is at `/workspace/source`.",
			Facts:       []briefFact{{"Identifier", sp.Project}},
		})
	if in.Repository == nil {
		return b, nil
	}
	state, err := domain.WikiStateOf(ctx, q, sp.Project)
	if err != nil {
		return runBrief{}, err
	}
	git := briefPart{
		Heading:     partGitState,
		Explanation: "The repository starts at the accepted `Start commit`. Use these commits for the coverage rules in the openwiki skill.",
		Facts:       []briefFact{{"Start commit", in.Repository.InputCommit}},
	}
	last := state.LastSucceeded
	if last != nil {
		git.Facts = append(git.Facts, briefFact{"Start commit of the last successful wiki update", last.StartCommit})
	}
	b.Parts = append(b.Parts, git)
	if last != nil {
		merged, err := mergedTasksPart(ctx, q, sp.Project, last.MergeMarker)
		if err != nil {
			return runBrief{}, err
		}
		if merged != nil {
			b.Parts = append(b.Parts, *merged)
		}
	}
	return b, nil
}

// mergedTasksBudget is the largest size of the JSON block of the Merged tasks
// part, in bytes.
const mergedTasksBudget = 262144

type briefMergedTask struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// mergedTasksPart lists tasks merged after the last successful wiki update.
func mergedTasksPart(ctx context.Context, q Q, project string, lower int64) (*briefPart, error) {
	upper, err := domain.WikiMergeMarker(ctx, q, project)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `
		WITH merged(task, seq) AS (
			SELECT m.task_id, r.created_seq FROM merge_operations m JOIN runs r ON r.id=m.run_id
			WHERE m.project=? AND m.operation='apply' AND m.finished=1 AND m.destination_branch='main'
			  AND m.task_id IS NOT NULL AND json_extract(m.result,'$.applied.commit') IS NOT NULL
			  AND r.created_seq>? AND r.created_seq<=?)
		SELECT t.id, t.title, COALESCE(t.description,'')
		FROM merged JOIN tasks t ON t.id=merged.task
		GROUP BY t.id ORDER BY MIN(merged.seq), t.id`, project, lower, upper)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []briefMergedTask{}
	for rows.Next() {
		var task briefMergedTask
		if err := rows.Scan(&task.ID, &task.Title, &task.Description); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return nil, nil
	}
	// The JSON block of a shorter list is smaller, so a binary search finds
	// the first row of the newest rows that fit.
	var blockErr error
	omitted := sort.Search(len(tasks), func(first int) bool {
		block, err := marshalBriefRows(tasks[first:])
		if err != nil {
			blockErr = err
		}
		return len(block) <= mergedTasksBudget
	})
	if blockErr != nil {
		return nil, blockErr
	}
	part := &briefPart{
		Heading:     partMergedTasks,
		Explanation: "These tasks merged after the last successful wiki update, oldest first. `Omitted tasks` counts older rows excluded from this part.",
		Rows:        tasks[omitted:],
	}
	if omitted > 0 {
		part.Facts = []briefFact{{"Omitted tasks", strconv.Itoa(omitted)}}
	}
	return part, nil
}

var subjectExplanation = map[dispatch.Role]string{
	dispatch.RoleWorker:   "Meet each criterion in the description and pass new or changed tests. Resolve task regressions that the Verifier identifies.",
	dispatch.RoleVerifier: "Verify each criterion and new or changed test on the submitted commit. Run broad checks and compare submitted failures with the base commit to identify task regressions.",
	dispatch.RolePackager: "The packet covers each criterion in this description. The dashboard supplies the page style.",
}

func addSubjectParts(ctx context.Context, q Q, b *runBrief, sp *dispatch.Spawn, task briefTaskRecord, in briefInputs) error {
	role := sp.Role
	facts := []briefFact{{"Identifier", strconv.FormatInt(task.ID, 10)}, {"Title", task.Title}}
	if isRole(role, dispatch.RoleWorker, dispatch.RoleVerifier) {
		facts = append(facts, briefFact{"Unsuccessful Verifier corrections", strconv.Itoa(task.CorrectionCount)})
	}
	if role == dispatch.RolePackager {
		facts = append(facts, briefFact{"Requirements version", strconv.FormatInt(task.RequirementsVersion, 10)})
	}
	subject := briefPart{Heading: partTask, Explanation: subjectExplanation[role], Facts: facts}
	if role != dispatch.RoleArbiter {
		subject.TextTag, subject.Text = "description", task.Description
	}
	b.Parts = append(b.Parts, subject)
	if role == dispatch.RoleWorker {
		part, err := requirementsRevisionPart(ctx, q, task)
		if err != nil {
			return err
		}
		if part != nil {
			b.Parts = append(b.Parts, *part)
		}
	}
	if isRole(role, dispatch.RoleWorker, dispatch.RoleVerifier) {
		b.Parts = append(b.Parts, briefPart{Heading: "Correction allowances", Explanation: "The Verifier correction allowance limits rejected corrections. These rules govern returned work.", Rows: []string{
			"The initial Verifier rejection starts a sequence at zero. Three subsequent rejected corrections require an operator grant.",
			"A Verifier pass restores the allowance. A requirements revision or explicit grant also restores it.",
			"Each merge conflict starts a merge repair. A merge repair clears the operator approval of its task.",
			"An operator packet revision preserves the allowance. A run retry uses the separate task run failure allowance.",
		}})
	}

	if task.OperatorReason != "" && isRole(role, dispatch.RoleWorker, dispatch.RoleVerifier) {
		b.Parts = append(b.Parts, briefPart{Heading: "Operator allowance explanation", Explanation: "The operator authorized more work for the unresolved condition. Independent controls remain effective.", TextTag: "operator_explanation", Text: task.OperatorReason})
	}
	if task.RevisionNotes != "" && isRole(role, dispatch.RoleWorker) {
		part, err := returnedWorkPart(ctx, q, role, task)
		if err != nil {
			return err
		}
		if part != nil {
			b.Parts = append(b.Parts, *part)
		}
	}
	if err := addVerdictParts(ctx, q, b, sp, task); err != nil {
		return err
	}
	if err := addLastReportPart(ctx, q, b, role, task); err != nil {
		return err
	}
	return addGitStateParts(ctx, q, b, sp, task, in.Repository)
}

// requirementsRevisionPart holds the reason of the applied requirements
// revision that produced the current requirements version of the task. Each
// Worker run on that version receives it.
func requirementsRevisionPart(ctx context.Context, q Q, task briefTaskRecord) (*briefPart, error) {
	var reason string
	err := q.QueryRowContext(ctx, `SELECT reason FROM requirements_revisions
 WHERE task_id=? AND state='applied' AND previous_requirements_version=?
 ORDER BY id DESC LIMIT 1`, task.ID, task.RequirementsVersion-1).Scan(&reason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &briefPart{
		Heading:     partRequirementsRevision,
		Explanation: "The operator changed the requirements of this task for this reason. The description holds every requirement.",
		Facts:       []briefFact{{"Requirements version", strconv.FormatInt(task.RequirementsVersion, 10)}},
		TextTag:     "revision_reason", Text: reason,
	}, nil
}

// returnedWorkPart states why the work returned to its producer. The record
// holds one reason text for three cases, and the part names the case, so the
// agent does not read the case from the shape of the text.
func returnedWorkPart(ctx context.Context, q Q, role dispatch.Role, task briefTaskRecord) (*briefPart, error) {
	latest, err := loadLatestVerdict(ctx, q, task.ID)
	if err != nil {
		return nil, err
	}
	// The Correction from the Verifier part holds the correction file.
	if task.ReturnedKind == "verifier" && latest != nil && latest.Outcome == "correct" {
		return nil, nil
	}
	if candidate, ok := mergeCandidate(task.RevisionNotes); ok && task.ReturnedKind == "merge" {
		explanation := "The merge with the destination branch had a conflict. " +
			commandSkillSentence(role, "holds the repair procedure")
		if role == dispatch.RoleWorker {
			explanation = "The merge with the destination branch had a conflict. " +
				"Keep the intent of both changes in one commit, or report an operator decision when the changes contradict."
		}
		return &briefPart{Heading: partMergeCandidate, Explanation: explanation, Rows: candidate}, nil
	}
	return &briefPart{
		Heading:     partRevision,
		Explanation: "The operator returned the review packet with this request. You must meet the request in this run.",
		TextTag:     "revision_request", Text: task.RevisionNotes,
	}, nil
}

// mergeCandidate reads the reason text of a merge conflict.
func mergeCandidate(reason string) (repository.Candidate, bool) {
	var candidate repository.Candidate
	ok := json.Unmarshal([]byte(reason), &candidate) == nil && candidate.Status != ""
	return candidate, ok
}

// commandSkillSentence names the skill that holds a procedure, so the part that
// starts a case leads the agent to the text for that case.
func commandSkillSentence(role dispatch.Role, clause string) string {
	name, err := RoleSkillName(role)
	if err != nil {
		return ""
	}
	return "The " + name + " skill " + clause + "."
}

func addVerdictParts(ctx context.Context, q Q, b *runBrief, sp *dispatch.Spawn, task briefTaskRecord) error {
	role := sp.Role
	latest, err := loadLatestVerdict(ctx, q, task.ID)
	if err != nil {
		return err
	}
	// Only the latest verdict can require a correction. A later pass resolves
	// the correction before it.
	if latest != nil && latest.Outcome == "correct" &&
		isRole(role, dispatch.RoleWorker, dispatch.RoleVerifier) {
		explanation := "The correction report states the problem, evidence, and proposed resolution. A rejected correction consumes one attempt; a pass restores the Verifier allowance."
		if isRole(role, dispatch.RoleWorker) {
			explanation = "The Verifier rejected the last submission. " +
				commandSkillSentence(role, "holds the correction procedure")
		}
		b.Parts = append(b.Parts, briefPart{
			Heading: partCorrection, Explanation: strings.TrimSpace(explanation),
			Facts: []briefFact{
				{"Correction file", latest.CorrectionPath},
				{"Evidence file", latest.EvidencePath},
				{"Requirements version", strconv.FormatInt(latest.RequirementsVersion, 10)},
			},
		})
	}
	if latest != nil && role == dispatch.RolePackager {
		b.Parts = append(b.Parts, briefPart{
			Heading:     partLatestVerdict,
			Explanation: "This is the most recent Verifier verdict on the current requirements of this work. Preserve unchanged failures and unverified checks as information in the Review Packet, without an additional approval requirement.",
			Facts:       verdictFacts(*latest),
		})
	}
	return nil
}

func verdictFacts(v briefVerdict) []briefFact {
	facts := []briefFact{{"Outcome", v.Outcome}, {"Requirements version", strconv.FormatInt(v.RequirementsVersion, 10)}}
	if v.EvidencePath != "" {
		facts = append(facts, briefFact{"Evidence file", v.EvidencePath})
	}
	if v.CorrectionPath != "" {
		facts = append(facts, briefFact{"Correction file", v.CorrectionPath})
	}
	return facts
}

func addLastReportPart(ctx context.Context, q Q, b *runBrief, role dispatch.Role, task briefTaskRecord) error {
	producer := "worker"
	var output sql.NullString
	var requirements int64
	err := q.QueryRowContext(ctx, `
		SELECT r.output_path, rr.requirements_version FROM runs r
		JOIN run_requirements rr ON rr.run_id=r.id AND rr.task_id=r.subject
		WHERE r.subject = ? AND r.role = ? AND r.output_path IS NOT NULL
		ORDER BY created_at DESC, created_seq DESC LIMIT 1`, task.ID, producer).Scan(&output, &requirements)
	if err == sql.ErrNoRows || (err == nil && !output.Valid) {
		return nil
	}
	if err != nil {
		return err
	}
	explanation := "The producer of this work wrote this report in its last run. It is a claim that you must check, not evidence."
	if baseRole(string(role)) == producer {
		explanation = "Your role wrote this report in its last run on this work. Read it before you continue."
	}
	b.Parts = append(b.Parts, briefPart{
		Heading: partLastReport, Explanation: explanation,
		Facts: []briefFact{{"File", output.String}, {"Requirements version", strconv.FormatInt(requirements, 10)}},
	})
	return nil
}

// addGitStateParts gives a role the delivery facts that it can use. A fact
// without a value is absent.
func addGitStateParts(ctx context.Context, q Q, b *runBrief, sp *dispatch.Spawn, task briefTaskRecord, repo *substrate.DispatchRepository) error {
	role := sp.Role
	if !isRole(role, dispatch.RoleWorker, dispatch.RoleVerifier, dispatch.RolePackager) {
		return nil
	}
	d, err := loadBriefDelivery(ctx, q, task.ID)
	if err != nil {
		return err
	}
	// The repository plan holds the input commit of this run.
	if repo != nil && repo.InputCommit != "" {
		d.InputCommit = repo.InputCommit
	}
	correction := d.CorrectionReason != ""
	var facts []briefFact
	add := func(label, value string) {
		if value != "" {
			facts = append(facts, briefFact{label, value})
		}
	}
	add("Start commit", d.InputCommit)
	if isRole(role, dispatch.RoleWorker, dispatch.RoleVerifier) {
		add("Base commit", d.BaseCommit)
	}
	if role == dispatch.RoleWorker {
		add("Destination branch", d.DestinationBranch)
	}
	if isRole(role, dispatch.RoleVerifier, dispatch.RolePackager) || (role == dispatch.RoleWorker && correction) {
		add("Submitted commit", d.SubmittedCommit)
		if d.VersionID.Valid {
			add("Submitted version", strconv.FormatInt(d.VersionID.Int64, 10))
		}
	}
	if role == dispatch.RoleWorker {
		if d.CheckpointVersion.Valid {
			add("Checkpoint version", strconv.FormatInt(d.CheckpointVersion.Int64, 10))
		}
	}
	if len(facts) > 0 {
		explanation := "These Git facts identify the task repository."
		switch {
		case role == dispatch.RolePackager:
			explanation += " The Files changed view of the dashboard shows the commit identifiers and the complete diff to the operator."
		case d.InputCommit != "":
			explanation += " Your repository at `/workspace/source` starts at the start commit."
		}
		b.Parts = append(b.Parts, briefPart{Heading: partGitState, Explanation: explanation, Facts: facts})
	}
	if role == dispatch.RolePackager {
		previous, err := loadPreviousPacket(ctx, q, task.ID, d.VersionID)
		if err != nil {
			return err
		}
		if previous != nil {
			b.Parts = append(b.Parts, *previous)
		}
	}
	return nil
}

// loadPreviousPacket returns the part for the newest packet that Culler
// published for another submitted version of the work.
func loadPreviousPacket(ctx context.Context, q Q, task int64, current sql.NullInt64) (*briefPart, error) {
	var path, commit string
	err := q.QueryRowContext(ctx, `SELECT p.render_path,v.result_commit FROM review_versions p JOIN task_versions v ON v.id=p.version_id
 WHERE p.task_id=? AND p.version_id IS NOT ? ORDER BY p.id DESC LIMIT 1`, task, current).Scan(&path, &commit)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &briefPart{
		Heading:     partPreviousPacket,
		Explanation: "Culler published this packet for an earlier submitted commit of this work. " + commandSkillSentence(dispatch.RolePackager, "holds the procedure for the new packet"),
		Facts:       []briefFact{{"File", path}, {"Submitted commit", commit}},
	}, nil
}

func loadBriefDelivery(ctx context.Context, q Q, task int64) (briefDelivery, error) {
	var d briefDelivery
	err := q.QueryRowContext(ctx, `SELECT COALESCE(d.base_commit,''),COALESCE(d.input_commit,''),COALESCE(v.result_commit,''),d.submitted_version,d.destination_branch,
 COALESCE(d.correction_reason,''),d.checkpoint_version
 FROM tasks d LEFT JOIN task_versions v ON v.id=d.submitted_version
 WHERE d.id=?`, task).Scan(&d.BaseCommit, &d.InputCommit, &d.SubmittedCommit, &d.VersionID, &d.DestinationBranch,
		&d.CorrectionReason, &d.CheckpointVersion)
	return d, err
}

func addArbiterParts(ctx context.Context, q Q, b *runBrief, sp *dispatch.Spawn) error {
	b.Parts = append(b.Parts, projectPart(sp.Project,
		"Read this Project's committed files at the read-only repository copy."))
	pool := make([]briefBoardRow, 0, len(sp.ProposedPool))
	for _, id := range sp.ProposedPool {
		task, err := loadBriefTask(ctx, q, id)
		if err != nil {
			return err
		}
		pool = append(pool, boardRow(task))
	}
	b.Parts = append(b.Parts, briefPart{
		Heading:     partPool,
		Explanation: "This fixed proposal set is your complete assignment, with no task subject. Cover each proposal and reject invalid criteria with the permitted rejection category.",
		Rows:        pool,
	})
	// Every promoted, unmerged task of this Project, blocked rows included: a
	// blocked task dispatches to nobody and is exactly the work a second
	// proposal repeats. Unbounded on purpose, because a dropped row is a
	// promoted duplicate. A merged task is absent: the repository shows it.
	active, err := loadBoardRows(ctx, q, `
		SELECT id FROM tasks
		WHERE archived = 0 AND status <> 'proposed' AND project = ?
		ORDER BY stage_rank DESC, priority, created_at, id`, sp.Project)
	if err != nil {
		return err
	}
	b.Parts = append(b.Parts, briefPart{
		Heading:     partActiveTasks,
		Explanation: "Each active task of this Project: in progress, blocked, or approved and not yet merged. The repository copy does not show this work, so check each proposal against these rows for overlap.",
		Rows:        active,
	})
	return nil
}

// projectPart names the Project and the read-only copy of its committed tree.
func projectPart(project, explanation string) briefPart {
	return briefPart{
		Heading:     partProject,
		Explanation: explanation,
		Facts:       []briefFact{{"Identifier", project}, {"Repository copy", "/workspace/seeding/" + project + "/source"}},
	}
}

func addProposeParts(ctx context.Context, q Q, b *runBrief, sp *dispatch.Spawn) error {
	project := projectPart(sp.Project,
		"Read this Project's committed files at the read-only repository copy.")
	b.Parts = append(b.Parts, project)

	// A blocked proposal stays in this list: it dispatches to nobody, so it is
	// the row that a Strategist proposes again and never sees judged.
	rows, err := q.QueryContext(ctx, `
		SELECT id, title, status FROM tasks
		WHERE archived = 0 AND project = ?
		ORDER BY stage_rank DESC, priority, created_at, id`, sp.Project)
	if err != nil {
		return err
	}
	defer rows.Close()
	active := []briefActiveTitle{}
	for rows.Next() {
		var t briefActiveTitle
		if err := rows.Scan(&t.ID, &t.Title, &t.Status); err != nil {
			return err
		}
		active = append(active, t)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	b.Parts = append(b.Parts, briefPart{
		Heading:     partActiveTasks,
		Explanation: "Each proposal and each active task of this Project, by title. Do not propose work that one of these rows covers; `mc task get <id>` returns the description of a row.",
		Rows:        active,
	})
	return nil
}

func addHistoricalParts(ctx context.Context, q Q, b *runBrief, project string) error {
	cancelled, err := loadHistoricalTasks(ctx, q, project, "cancelled", 10)
	if err != nil {
		return err
	}
	rejected, err := loadHistoricalTasks(ctx, q, project, "rejected", 20-len(cancelled))
	if err != nil {
		return err
	}
	if len(rejected) > 0 {
		b.Parts = append(b.Parts, briefPart{
			Heading:     partRejected,
			Explanation: "These recent rejected tasks fill unused places after cancellations, up to 20 historical tasks total, with the newest decisions first. Each entry contains the task number, title, and decision; use it to assess repeated work.",
			Rows:        rejected,
		})
	}
	if len(cancelled) > 0 {
		b.Parts = append(b.Parts, briefPart{
			Heading:     partCancelled,
			Explanation: "This limited history contains up to 10 recent cancelled tasks within the total limit of 20, newest decisions first. Each entry contains the task number, title, decision, and available reason excerpt of at most 160 Unicode characters.",
			Rows:        cancelled,
		})
	}
	return nil
}

func loadHistoricalTasks(ctx context.Context, q Q, project, decision string, limit int) ([]briefHistoricalTask, error) {
	rows, err := q.QueryContext(ctx, `
		SELECT t.id, t.title, t.decision,
			CASE WHEN t.decision = 'cancelled' THEN COALESCE((
				SELECT a.detail FROM activity a
				WHERE a.kind = 'task.cancelled' AND a.subject = CAST(t.id AS TEXT)
				ORDER BY a.id DESC LIMIT 1
			), '') ELSE '' END
		FROM tasks t WHERE t.project = ? AND t.decision = ?
		ORDER BY t.decided_at DESC, t.id DESC LIMIT ?`, project, decision, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []briefHistoricalTask
	for rows.Next() {
		var task briefHistoricalTask
		if err := rows.Scan(&task.ID, &task.Title, &task.Decision, &task.Reason); err != nil {
			return nil, err
		}
		task.Reason = cancellationExcerpt(task.Reason)
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func cancellationExcerpt(reason string) string {
	text := []rune(strings.Join(strings.Fields(reason), " "))
	if len(text) > 160 {
		return string(text[:159]) + "…"
	}
	return string(text)
}

var visionExplanation = map[dispatch.Role]string{
	dispatch.RoleStrategist: "This is the current content of `VISION.md`, the goal of the Project. Propose two tasks that advance this goal.",
	dispatch.RoleArbiter:    "This is the current content of `VISION.md`, the goal of the Project. Rank the two task proposals by how far each advances this goal.",
}

// addVisionPart gives the vision to the roles that select and plan work. The
// other roles receive a task that already states its criteria.
func addVisionPart(b *runBrief, vision *ProjectVisionCheck) {
	explanation, ok := visionExplanation[b.Role]
	if !ok || vision == nil || !vision.Ready {
		return
	}
	b.Parts = append(b.Parts, briefPart{Heading: partVision, Explanation: explanation, TextTag: "vision", Text: vision.Content})
}

var roleTitle = map[dispatch.Role]string{
	dispatch.RoleStrategist: "Strategist",
	dispatch.RoleArbiter:    "Arbiter",
	dispatch.RoleWorker:     "Worker",
	dispatch.RoleVerifier:   "Verifier",
	dispatch.RoleScribe:     "Scribe",
	dispatch.RolePackager:   "Packager",
}

// renderRunBrief writes the brief as a text document. Each part has the same
// form: a heading, an explanation, then facts, prose, or one JSON block.
func renderRunBrief(b runBrief) (string, error) {
	var w strings.Builder
	fmt.Fprintf(&w, "%s (%s)\n\n# %s run %s\n\n", spawnBriefHeader, spawnBriefSchema, roleTitle[b.Role], b.RunID)
	fmt.Fprintf(&w, "## Standard Culler directive\n\n<role_directive>\n%s\n</role_directive>\n", strings.TrimSpace(b.Directive))
	for _, p := range b.Parts {
		fmt.Fprintf(&w, "\n## %s\n\n%s\n", p.Heading, p.Explanation)
		if len(p.Facts) > 0 {
			w.WriteString("\n")
			for _, f := range p.Facts {
				fmt.Fprintf(&w, "- %s: %s\n", f.Label, f.Value)
			}
		}
		if p.Text != "" {
			fmt.Fprintf(&w, "\n<%s>\n%s\n</%s>\n", p.TextTag, strings.TrimSpace(p.Text), p.TextTag)
		}
		if p.Rows != nil {
			data, err := marshalBriefRows(p.Rows)
			if err != nil {
				return "", fmt.Errorf("render run brief part %q: %w", p.Heading, err)
			}
			fmt.Fprintf(&w, "\n```json\n%s\n```\n", data)
		}
	}
	return w.String(), nil
}

// marshalBriefRows keeps `<`, `>`, and `&` as written: the reader is a person
// or an agent, never an HTML page.
func marshalBriefRows(rows any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rows); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

func boardRow(t briefTaskRecord) briefBoardRow {
	row := briefBoardRow{
		ID: t.ID, Title: t.Title, Description: t.Description, Scope: t.Scope,
		Priority: t.Priority, Status: t.Status, Blocked: t.Blocked, BlockedReason: t.BlockedReason,
		Decision: t.Decision, Archived: t.Archived,
	}
	return row
}

func loadBriefTask(ctx context.Context, q Q, id int64) (briefTaskRecord, error) {
	var task briefTaskRecord
	var blocked, archived int
	err := q.QueryRowContext(ctx, `
		SELECT id, title, COALESCE(description, ''), scope,
		       priority, status, correction_count,COALESCE(returned_kind,''),COALESCE(correction_operator_reason,''), blocked,
		       COALESCE(blocked_reason, ''),
		       COALESCE(revision_notes, ''), COALESCE(decision, ''), archived, record_version, requirements_version
		FROM tasks WHERE id = ?`, id).Scan(
		&task.ID, &task.Title, &task.Description, &task.Scope,
		&task.Priority, &task.Status, &task.CorrectionCount, &task.ReturnedKind, &task.OperatorReason, &blocked,
		&task.BlockedReason, &task.RevisionNotes,
		&task.Decision, &archived, &task.RecordVersion, &task.RequirementsVersion)
	if err != nil {
		return task, err
	}
	task.Blocked = blocked == 1
	task.Archived = archived == 1
	return task, nil
}

func loadBoardRows(ctx context.Context, q Q, query string, args ...any) ([]briefBoardRow, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Non-nil when empty: an empty board is an answer and renders as [].
	board := make([]briefBoardRow, 0, len(ids))
	for _, id := range ids {
		task, err := loadBriefTask(ctx, q, id)
		if err != nil {
			return nil, err
		}
		board = append(board, boardRow(task))
	}
	return board, nil
}

// loadLatestVerdict returns the newest verdict on the current requirements.
func loadLatestVerdict(ctx context.Context, q Q, taskID int64) (*briefVerdict, error) {
	var verdict briefVerdict
	err := q.QueryRowContext(ctx, `SELECT r.verdict_outcome,COALESCE(r.evidence_path,''),COALESCE(r.correction_path,''),rr.requirements_version
 FROM runs r JOIN run_requirements rr ON rr.run_id=r.id AND rr.task_id=r.subject
 JOIN tasks t ON t.id=r.subject WHERE r.subject=? AND r.role='verifier' AND r.verdict_outcome IS NOT NULL
 AND rr.requirements_version=t.requirements_version
 ORDER BY r.created_at DESC,r.created_seq DESC LIMIT 1`, taskID).
		Scan(&verdict.Outcome, &verdict.EvidencePath, &verdict.CorrectionPath, &verdict.RequirementsVersion)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &verdict, nil
}
