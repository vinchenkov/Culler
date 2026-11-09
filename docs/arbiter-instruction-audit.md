# Arbiter instruction audit

The directive holds review rules.
The command skill holds command forms and conditional procedures.
The command forms, runtime authority, proposal records, and operator decisions remain unchanged.

## Scope and method

The assigned base is `b187b445bb3ac33e0489dc56b76c27c68e9127cf`.
The final counts describe the files in the commit that contains this report.
The audit covers the Arbiter directive, its sole role-specific skill, brief explanations, and generated skill pointers.
No role-specific skill was added or removed.
Shared skill contents are outside this audit.
Their references and delivery remain unchanged.
Other roles' directives and skills remain unchanged.

The structure follows [Matt Pocock's writing-for-agents reference](https://github.com/mattpocock/skills/tree/321658273cb1d20b76026717d027d505790106d4/skills/productivity/writing-for-agents).
The review used `SKILL.md` and `SKILL-MECHANICS.md` at that revision.
Each rule has one home; conditional procedures have explicit triggers.
The existing writing skill remains the language standard.
A read-only subagent reviewed the reference and compared the edited instructions with the base.
The final selection and edits remained with this Worker.

## Word counts

Both counts use Python `len(text.split())` on each complete UTF-8 file.
This method counts whitespace-separated tokens, including frontmatter, headings, list markers, and command examples.
The base text comes from `git show <base>:<path>`; the final text comes from the checkout.

| File | Before | After |
| --- | ---: | ---: |
| `mc/verbs/directives/arbiter.md` | 2029 | 545 |
| `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md` | 1576 | 612 |
| Total | 3605 | 1157 |

## Why each section remains

The title of each file identifies the role.
The skill frontmatter supplies its discovery name and procedure summary.

| File and section | Concrete purpose |
| --- | --- |
| Directive: Culler contract | Bounds the verdict set and makes the promotion limit and criteria rules contract requirements. Preserves evidence, restricts authority, and requires a terminal. |
| Directive: Project role customization | Distinguishes operator defaults from contract requirements and points conflicts to their procedure. |
| Directive: Proposal review | Defines rank, promotion limits, criteria validity, available capabilities, and overlap judgments. |
| Directive: Cases | States the conditions that require history, all-reject, dependency, or failure procedures. |
| Directive: Orchestration | Bounds subagent work, preserves read-only access, and keeps selection with the Arbiter. |
| Skill: Terminal action | Gives the verdict command and exact JSON shape, including rejection fields and refusal recovery. |
| Skill: The all-reject batch | Defines ready tasks, the two exceptions, valid duplicate references, and when an operator decision is necessary. |
| Skill: Historical work | Requires comparison with rejected and cancelled work and use of cancellation reasons as operator context. |
| Skill: Research dependencies | Prevents reliance on automatic preparation and requires compatibility checks before dependency use. |
| Skill: A run that cannot give a result | Keeps a verdict preferable when possible and separates operator decisions from execution problems. |
| Skill: A decision that only the operator can make | Gives both report forms and the affected-subset rule; proposals wait for the operator's resolution. |
| Skill: An execution problem | Gives both failure commands and requires direct observations while preserving work after connection recovery. |
| Skill: Reads | Lists the permitted read commands and identifies when a task description needs a separate read. |
| Skill: Project role customization | Requires the conflict terminal, states that it ends the run, and gives each required request field. |

The verdict schema permits only promotion or rejection.
The directive limits the verdict set to the supplied pool.
The all-reject procedure permits mixed exceptions and excludes blocked or stalled tasks from the ready set.
Only active or merged tasks of the same Project can serve as duplicate references.
That rule excludes rejected and cancelled tasks without a second prohibition.
No new confirmation rule replaces the operator decision procedure.

## Generated explanations and pointers

Only Arbiter output changes in the common generators.
`trimArbiterBrief` removes two explanatory parts and replaces selected explanations.
It preserves assignment facts, text, rows, operator customization, proposal resolutions, and repository paths.
`skillPointer` selects the shorter command pointer only for `mc-arbiter`.
No shared skill description or path changes.

| Retained part or pointer | Concrete purpose |
| --- | --- |
| Project role customization | Associates operator instructions with their revision. |
| Run | Maps the supplied identifier to command placeholders. |
| Project environment | Identifies the selected environment for the conditional dependency check. |
| Project | Directs repository reads to the supplied read-only copy. |
| Proposed pool | Identifies the assignment and defines the priority field as proposer urgency. |
| Active tasks | Includes blocked and approved tasks in the scope comparison before they merge. |
| Rejected work | Identifies a recent historical selection. |
| Cancelled work | Identifies recent history and the optional reason excerpts. |
| Proposal decisions | Requires each relevant operator resolution. |
| Vision | Identifies the accepted goal used for rank. |
| Your command surface | Gives the installed skill path and the conditions for its required read. |
| Your other skills | Preserves discovery of shared skills and their existing conditions. |

The Project and Proposal decisions explanations remain byte-identical.
The following ledger records each changed explanation or pointer with its original text.

### `mc/verbs/brief.go` — Project role customization

Category: `duplicate`.
Retained authority: `mc/verbs/directives/arbiter.md`, Project role customization.
Retained completion rule: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, Project role customization.

Original text:

```text
These operator instructions replace standard defaults within the Culler contract. A contract conflict ends the run with a customization conflict.
```

Retained text:

```text
This part identifies the operator's custom instructions and their revision.
```

### `mc/verbs/brief.go` — Run

Category: `duplicate`.
Retained mapping: `mc/verbs/brief.go`, trimArbiterBrief, Run.

Original text:

```text
This value identifies this run. It is the `<run_id>` value of each `mc` command.
```

Retained text:

```text
Use this identifier as `<run_id>` in command forms that require it.
```

### `mc/verbs/brief.go` — Agent credentials

Category: `unneeded`.
Harness selection and credential mechanics provide no review action.

Original text:

```text
The agent profile selects the harness, model, and effort; the same role input applies to Codex, Claude, and OpenCode. Culler keeps provider credentials on the host and supplies this container with temporary proxy access.
```

### `mc/verbs/brief.go` — Project environment

Category: `unneeded`.
The old explanation describes assigned task runs. The Arbiter receives a projection and omits automatic preparation.

Original text:

```text
Culler supplies the selected image and persistent Project home. A task run receives a fresh source workspace and dependency preparation before the agent starts.
```

Retained text:

```text
These identifiers name the selected Project environment.
```

### `mc/verbs/brief.go` — Proposed pool

Category: `duplicate`.
Retained coverage and criteria rules: `mc/verbs/directives/arbiter.md`, Culler contract and Proposal review. The priority explanation moves here from that file.

Original text:

```text
This fixed proposal set is your complete assignment, with no task subject. Cover each proposal and reject invalid criteria with the permitted rejection category.
```

Retained text:

```text
These are the proposals for this assignment. `priority` records proposer urgency: -1 is expedited; 0 through 3 mean P0 through P3.
```

### `mc/verbs/brief.go` — Active tasks

Category: `duplicate`.
Retained comparison rule: `mc/verbs/directives/arbiter.md`, Proposal review. Record interpretation remains in this part.

Original text:

```text
Each active task of this Project: in progress, blocked, or approved and not yet merged. The repository copy does not show this work, so check each proposal against these rows for overlap.
```

Retained text:

```text
These are this Project's active tasks, including blocked tasks and approved tasks that await a merge.
```

### `mc/verbs/brief.go` — Rejected work: selection and fields

Category: `unneeded`.
Selection limits, order, and visible field names add no review action.

Original text:

```text
These recent rejected tasks fill unused places after cancellations, up to 20 historical tasks total, with the newest decisions first. Each entry contains the task number, title, and decision;
```

Retained text:

```text
These are recent rejected tasks.
```

### `mc/verbs/brief.go` — Rejected work: repeated work

Category: `moved behind a pointer`.
Destination: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, Historical work. Pointer condition: Rejected work or Cancelled work is present.

Original text:

```text
use it to assess repeated work.
```

### `mc/verbs/brief.go` — Cancelled work

Category: `unneeded`.
Selection counts, order, field names, and exact excerpt lengths do not change review behavior.

Original text:

```text
This limited history contains up to 10 recent cancelled tasks within the total limit of 20, newest decisions first. Each entry contains the task number, title, decision, and available reason excerpt of at most 160 Unicode characters.
```

Retained text:

```text
These are recent cancelled tasks, with reason excerpts when available.
```

### `mc/verbs/brief.go` — Failure controls

Category: `unneeded`.
The Arbiter reports the observed problem or required decision. It does not administer these controls.

Original text:

```text
Execution problems and operator decisions use separate controls. A confirmed connection condition uses the connection controls.

The runner confirms connection recovery from complete model responses. Recovery preserves the task outcome and its retry allowance.
Dispatch and launch failures use the Project infrastructure allowance before the agent process starts.
Strategist proposal creation and Arbiter proposal review share that Project allowance throughout their runs.
Assigned task failures use their task run failure allowance after the agent process starts.
Each failure allowance permits three counted failures. An operator decision report consumes zero failure units.
```

### `mc/verbs/brief.go` — Vision

Category: `duplicate`.
Retained rank rule: `mc/verbs/directives/arbiter.md`, Proposal review. The supplied pool, not an assumed count, defines the assignment.

Original text:

```text
This is the current content of `VISION.md`, the goal of the Project. Rank the two task proposals by how far each advances this goal.
```

Retained text:

```text
This is the accepted `VISION.md` content.
```

### `mc/verbs/directives.go` — Your command surface

Category: `duplicate`.
Retained pointer: `mc/verbs/directives.go`, skillPointer, Your command surface. The contract restricts calls to permitted commands.

Original text:

```text
Every `mc` command available to you, with its exact flags and constraints, is the mc-arbiter skill, installed in this container at `~/.agents/skills/mc-arbiter/SKILL.md`. Load it before your first `mc` call and again before you submit your terminal.

`mc` ships no help surface: bare `mc` names zero verbs and `mc --help` is an unknown verb. That skill is the complete surface your role may call. A verb absent from it does not exist for you, and a search for one wastes the run.
```

Retained text:

```text
Read `~/.agents/skills/mc-arbiter/SKILL.md` before your first `mc` call and before the terminal.
The skill lists the permitted commands; `mc` has no help command.
```

## Original passage ledger

Each entry quotes the base text exactly, including line breaks.
Line numbers refer to the assigned base, not this checkout.
A passage can include unchanged context when a replacement shortens the whole passage.
`duplicate` identifies a rule that remains at the named home, including a shorter replacement in the same section.
`unneeded` identifies text with no additional Arbiter action.
`moved behind a pointer` identifies a conditional procedure and its read trigger.
Headings that disappear travel with their sections; retained headings need no deletion entry.

### Passage 1: `mc/verbs/directives/arbiter.md`, lines 3–10

Category: `unneeded`.
Model selection, harness differences, credential storage, the proxy, and renewal give the Arbiter no review action.

```text
The agent profile sets the model and effort for this run.
Culler selects Codex, the Claude Agent Software Development Kit (SDK), or OpenCode as the harness.
The role contract and the installed skills apply to each harness.
OpenCode web search is available only when the selected provider supports it.

Culler keeps provider credentials on the host.
This container uses a temporary Culler token through the host provider proxy.
The host renews account access without a credential update inside the container.
```

### Passage 2: `mc/verbs/directives/arbiter.md`, lines 11–11

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract.
The retained safety rule covers supplied configuration and token output without proxy mechanics.

```text
Preserve the proxy configuration and exclude Culler tokens from output.
```

### Passage 3: `mc/verbs/directives/arbiter.md`, lines 13–18

Category: `unneeded`.
Permission comparisons, storage details, and other roles do not affect the verdict. The generated Project part supplies the repository boundary.

```text
Setup and pipeline agents share container permissions, resource limits, network policy, and workspace paths.
Role authority and credentials remain separate.

Culler preserves the Project home at `HOME` across Setup Agent runs and pipeline runs.
The home retains dependency caches, installed tools, and Project tool configuration.
This run receives a fresh committed projection and separate credentials, sessions, role skills, and instructions.
```

### Passage 4: `mc/verbs/directives/arbiter.md`, lines 19–20

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Research dependencies.
Pointer condition: review requires Project dependencies.

```text
This role omits automatic dependency preparation.
Check retained dependencies against the current declarations and the selected image before use.
```

### Passage 5: `mc/verbs/directives/arbiter.md`, lines 24–24

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract; Proposal review.
The exact assignment and promotion limit define the required selection without assuming two proposals.

```text
Select work from the two task proposals through the permitted decision. Preserve the pool, capacity limits, and proposal requirements.
```

### Passage 6: `mc/verbs/directives/arbiter.md`, lines 26–27

Category: `unneeded`.
The contract heading supplies the boundary. Pipeline topology gives no additional Arbiter action.

```text
The contract retains the pipeline topology and this role's responsibility.
The contract also retains these requirements:
```

### Passage 7: `mc/verbs/directives/arbiter.md`, lines 29–35

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract.
The retained contract states requirements, evidence, command authority, safety, and completion once.

```text
- Preserve the acceptance criteria and accepted work.
- Report evidence and results truthfully.
- Produce the required deliverables.
- Use `mc`, the Mission Control command, for Culler state changes.
- Use only permitted commands with your own run identity.
- Preserve repository, artifact, and display safety constraints.
- Submit exactly one permitted terminal action to end the run.
```

### Passage 8: `mc/verbs/directives/arbiter.md`, lines 39–39

Category: `duplicate`.
Retained home or destination: mc/verbs/brief.go — trimArbiterBrief, Project role customization.
The part explanation identifies operator instructions and their revision.

```text
The **Project role customization** part contains the operator's instructions for this Project and role.
```

### Passage 9: `mc/verbs/directives/arbiter.md`, lines 40–42

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Project role customization.
One sentence defines operator precedence across all three input locations.

```text
Follow them within your Culler contract.
They replace standard defaults where those defaults differ.
Custom instructions also replace defaults in the run brief and role skills.
```

### Passage 10: `mc/verbs/directives/arbiter.md`, lines 45–46

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Project role customization.
Pointer condition: a custom instruction conflicts with the contract.

```text
If an instruction conflicts with your contract, report a customization conflict and explain the conflict.
The command skill describes the `mc run customization-conflict` terminal.
```

### Passage 11: `mc/verbs/directives/arbiter.md`, lines 47–48

Category: `unneeded`.
Allowance accounting and revision-zero interpretation do not change the required response to custom instructions.

```text
This command preserves the Project pause state and all failure and correction allowances.
Revision zero means that the run received no custom instructions.
```

### Passage 12: `mc/verbs/directives/arbiter.md`, lines 50–50

Category: `unneeded`.
The pipeline tutorial adds other-role mechanics. The retained contract already defines this role and its terminal duty.

```text
Culler runs a pipeline of agent roles on one Project, a Git repository, for one human operator. The Strategist proposes tasks. The Arbiter selects them. The Worker builds each task. The Verifier judges the result. The Packager writes the review packet that the operator approves or rejects. Each role runs in a fresh container and knows only this run brief and the skills that it names. `mc` is the command-line program of Culler. A role reports its result with one `mc` command, the terminal action, and that command ends the run.
```

### Passage 13: `mc/verbs/directives/arbiter.md`, lines 52–59

Category: `unneeded`.
Role introductions and glossary entries repeat the supplied pool and board labels. The generated parts identify those records.

```text
You are the Arbiter. The Strategist supplies your input: the pool of proposals of this Project. You promote a proposal to accept it as work for the pipeline, or you reject it. A promoted task goes to the Worker.

These terms apply in this run:

- A proposal is a candidate task that the Strategist filed. Its `scope` value is `task`.
- The pool is the set of proposals of this Project that the Arbiter did not judge yet.
- An active task is a promoted task whose work is absent from the destination branch. The destination branch receives each approved change.
- Existing work is each active task and the merged work of this Project.
```

### Passage 14: `mc/verbs/directives/arbiter.md`, lines 60–61

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The retained overlap definition and rejection rule give the operative meaning directly.

```text
- A proposal overlaps a piece of work when both change the same files or the same behaviour.
- A duplicate is a proposal that overlaps existing work.
```

### Passage 15: `mc/verbs/directives/arbiter.md`, lines 62–62

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — The all-reject batch.
The conditional procedure defines readiness precisely.

```text
- The ready queue is the set of tasks of this Project that a role can start now.
```

### Passage 16: `mc/verbs/directives/arbiter.md`, lines 64–66

Category: `unneeded`.
The document structure needs no tutorial.

```text
## The run brief

The parts of the run brief follow this directive. A part is a section with a heading, an explanation, and data. A part that is absent does not apply to this run. You can receive these parts:
```

### Passage 17: `mc/verbs/directives/arbiter.md`, lines 68–68

Category: `duplicate`.
Retained home or destination: mc/verbs/brief.go — trimArbiterBrief, Run.
The generated explanation maps the run identifier once.

```text
- The **Run** part holds the run identifier. It is the `<run_id>` value of each `mc` command.
```

### Passage 18: `mc/verbs/directives/arbiter.md`, lines 69–69

Category: `duplicate`.
Retained home or destination: mc/verbs/brief.go — trimArbiterBrief, Proposed pool; mc/verbs/directives/arbiter.md — Culler contract.
The generated part identifies the assignment; the directive bounds its verdict set.

```text
- The **Proposed pool** part defines the complete assignment. This Project proposal run has no task subject.
```

### Passage 19: `mc/verbs/directives/arbiter.md`, lines 70–70

Category: `duplicate`.
Retained home or destination: mc/verbs/brief.go — buildRunBrief, Proposal decisions.
The unchanged explanation requires relevant operator resolutions.

```text
- The **Proposal decisions** part holds the operator resolutions for Project proposal questions. Apply each relevant resolution.
```

### Passage 20: `mc/verbs/directives/arbiter.md`, lines 71–71

Category: `duplicate`.
Retained home or destination: mc/verbs/brief.go — addArbiterParts, Project.
The unchanged explanation names the read-only repository copy.

```text
- The **Project** part names the Project and its read-only `Repository copy`. Read source files and project knowledge from that copy.
```

### Passage 21: `mc/verbs/directives/arbiter.md`, lines 72–74

Category: `duplicate`.
Retained home or destination: mc/verbs/brief.go — trimArbiterBrief, Active tasks, Rejected work, and Cancelled work.
The generated parts identify their records.

```text
- The **Active tasks** part holds each active task, with its full description.
- The **Rejected work** part holds compact entries for recent rejected tasks of this Project.
- The **Cancelled work** part holds compact entries for recent cancelled tasks of this Project.
```

### Passage 22: `mc/verbs/directives/arbiter.md`, lines 75–75

Category: `duplicate`.
Retained home or destination: mc/verbs/brief.go — trimArbiterBrief, Vision; mc/verbs/directives/arbiter.md — Proposal review.
The explanation identifies accepted content; the directive uses its goal for rank.

```text
- The **Vision** part holds the goal of the Project, the content of its `VISION.md` file.
```

### Passage 23: `mc/verbs/directives/arbiter.md`, lines 77–81

Category: `unneeded`.
Selection counts, tie order, and omission rules do not change proposal review.

```text
The historical parts contain at most 20 tasks total.
Culler selects up to 10 cancelled tasks first.
Rejected tasks fill the unused places.
Each part lists the newest decisions first, with larger task numbers first for equal decision times.
An empty part is absent.
```

### Passage 24: `mc/verbs/directives/arbiter.md`, lines 82–82

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Historical work.
Pointer condition: Rejected work or Cancelled work is present.

```text
Older tasks remain available through the permitted read commands.
```

### Passage 25: `mc/verbs/directives/arbiter.md`, lines 84–91

Category: `unneeded`.
The records show their fields. Excerpt lengths and omitted descriptions require no separate rule.

```text
Each historical entry contains these fields:

- The task number.
- The recorded title.
- The decision.

A cancelled entry also contains a recorded reason excerpt when available, with at most 160 Unicode characters.
Historical entries exclude descriptions.
```

### Passage 26: `mc/verbs/directives/arbiter.md`, lines 92–92

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Reads.
The task read supplies scope absent from the pool and active-task descriptions.

```text
Use `mc task get <id>` when a historical title leaves the scope unclear.
```

### Passage 27: `mc/verbs/directives/arbiter.md`, lines 93–93

Category: `unneeded`.
The full descriptions remain in the supplied records; another format description does not guide an action.

```text
The **Active tasks** and **Proposed pool** parts retain full descriptions for comparison of scope and success criteria.
```

### Passage 28: `mc/verbs/directives/arbiter.md`, lines 95–100

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract; Proposal review.
The verdict boundary, authority limits, and comparison rule replace repeated pool instructions.

```text
## The pool

Compare the proposals of the **Proposed pool** part with each other. The part
holds the whole task proposal pool of this Project. The Strategist normally supplies two tasks. Promote or
reject every proposal of the part exactly once. You cannot defer a proposal,
merge two proposals, or rewrite a proposal.
```

### Passage 29: `mc/verbs/directives/arbiter.md`, lines 102–110

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The retained rules cover rank and criteria once, with a Project-neutral whole-suite example.

```text
- Rank each proposal by how far it advances the goal of the Project for its cost.
- Require from each proposal success criteria that the Worker and the Verifier can check in an agent container.
- Require criteria that describe behavior within the task's scope.
- New or changed tests must pass on the submitted commit.
- Reject invalid acceptance criteria with the `invalid_criteria` category.
- Whole-suite success requirements, such as `./mc/check.sh passes`, are invalid criteria.
- Required checks that need unavailable capabilities are invalid criteria.
- Check the capabilities of the required tests. An available suite command does not prove that its tests can run.
- State the invalid criterion and the rule that it violates in the rejection reason.
```

### Passage 30: `mc/verbs/directives/arbiter.md`, lines 111–111

Category: `unneeded`.
Another role owns broad regression checks; the Arbiter cannot perform that role’s duties.

```text
- Broad regression checks belong to the Verifier's duties for every task.
```

### Passage 31: `mc/verbs/directives/arbiter.md`, lines 112–118

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The retained capability list and one-commit requirement constrain feasible proposals.

```text
- No agent container has these capabilities:
  - host access;
  - a Docker daemon;
  - macOS system interfaces;
  - additional container privileges;
  - a run on a hosted continuous integration (CI) service, such as GitHub Actions.
- Each task delivers one Git commit to the Project repository. Reject a proposal whose result is not a commit.
```

### Passage 32: `mc/verbs/directives/arbiter.md`, lines 119–119

Category: `duplicate`.
Retained home or destination: mc/verbs/brief.go — trimArbiterBrief, Proposed pool.
The priority explanation belongs with the field; rank remains a separate directive rule.

```text
- The `priority` value of a row is the urgency that the Strategist stated: -1 is expedited, and 0 to 3 are P0 to P3. It is not your rank.
```

### Passage 33: `mc/verbs/directives/arbiter.md`, lines 120–121

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The promotion limit and goal-for-cost rule replace repeated rank explanations.

```text
- Promote exactly one proposal in each batch. Select the proposal with the best reward for the cost. Reject the other proposals. The one exception is a batch that rejects every proposal.
- A task with a high reward and low cost outranks a task with a lower reward and higher cost.
```

### Passage 34: `mc/verbs/directives/arbiter.md`, lines 123–123

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract.
The contract explicitly includes the criteria rules.

```text
These criteria rules belong to the Culler contract.
```

### Passage 35: `mc/verbs/directives/arbiter.md`, lines 124–124

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
An unchanged failure alone does not invalidate a proposal.

```text
An unchanged Project test failure alone permits a proposal that satisfies these rules.
```

### Passage 36: `mc/verbs/directives/arbiter.md`, lines 126–140

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — The all-reject batch.
Pointer condition: the Arbiter intends to reject every proposal.

```text
A batch can reject every proposal when another task is ready in this Project.
A task outside your pool is ready under these conditions:

- Its status is `proposed`, `seeded`, `worked`, or `verified`.
- It is not blocked.
- It is not stalled.

An empty ready queue requires one promotion unless every rejection declares one permitted exception:

- A valid `duplicate_of` reference to prior work in this Project.
- The `invalid_criteria` rejection category with a reason.

The batch can combine these exceptions across proposals.
Each rejection uses only one exception.
A low rank or general preference does not qualify.
```

### Passage 37: `mc/verbs/directives/arbiter.md`, lines 141–142

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Cases.
The condition and named destination appear in the case list.

```text
Read "The all-reject batch" in the mc-arbiter skill before you submit such a batch.
The skill states the reference checks and the procedure for other operator decisions.
```

### Passage 38: `mc/verbs/directives/arbiter.md`, lines 144–145

Category: `unneeded`.
Archival activity and later dispatch mechanics do not change the verdict.

```text
An accepted rejection archives the proposal and retains its reason in `task.rejected` activity.
A later Strategist run proposes new work under the normal dispatch rules.
```

### Passage 39: `mc/verbs/directives/arbiter.md`, lines 147–154

Category: `duplicate`.
Retained home or destination: mc/verbs/brief.go — trimArbiterBrief, Active tasks.
The part identifies active work, including blocked tasks and tasks that await a merge.

```text
## The board

The **Active tasks** part is the board of this Project outside your pool. It
holds these tasks:

- the tasks in progress at every stage;
- the blocked tasks;
- the approved tasks that wait to merge.
```

### Passage 40: `mc/verbs/directives/arbiter.md`, lines 156–158

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The comparison and same-Project rules preserve the board boundary.

```text
Merged work is outside the **Active tasks** part. Read merged work in the
repository copy. A proposal that overlaps merged work is a duplicate. Work in
another Project is not on this board and cannot be cited.
```

### Passage 41: `mc/verbs/directives/arbiter.md`, lines 160–163

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Historical work.
Pointer condition: Rejected work or Cancelled work is present.

```text
## Overlap

Compare each proposal with the **Rejected work** and **Cancelled work** parts.
Treat equivalent work as a reason to reject the proposal under the permitted batch rules.
```

### Passage 42: `mc/verbs/directives/arbiter.md`, lines 164–164

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The retained instruction judges scope rather than titles alone.

```text
A different title for equivalent work still counts as repeated work.
```

### Passage 43: `mc/verbs/directives/arbiter.md`, lines 165–165

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Historical work.
Pointer condition: Rejected work or Cancelled work is present.

```text
Use each cancellation reason as context for the operator's preference.
```

### Passage 44: `mc/verbs/directives/arbiter.md`, lines 166–166

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — The all-reject batch.
The single reference rule requires active or merged work of this Project.

```text
Historical tasks do not qualify as existing work for `duplicate_of`.
```

### Passage 45: `mc/verbs/directives/arbiter.md`, lines 167–167

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — The all-reject batch.
The procedure directs a required operator decision when its rules prevent a verdict.

```text
Use the operator decision procedure when the all-reject rules prevent a permitted verdict.
```

### Passage 46: `mc/verbs/directives/arbiter.md`, lines 169–169

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The retained rule rejects overlap with active work.

```text
Reject a proposal that overlaps an active task.
```

### Passage 47: `mc/verbs/directives/arbiter.md`, lines 169–170

Category: `unneeded`.
The merge-failure prediction adds no criterion.

```text
Both changes would land, and
the second change would not merge.
```

### Passage 48: `mc/verbs/directives/arbiter.md`, lines 170–173

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The retained scope and superset rules state the action.

```text
Judge overlap on the descriptions, the files
that each one names, and the behaviour that it changes. Do not judge overlap on
titles alone. When two valid proposals of the pool overlap, promote the superset and
reject the subset.
```

### Passage 49: `mc/verbs/directives/arbiter.md`, lines 175–177

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract; Proposal review.
The contract protects active tasks; the overlap rule rejects even a better proposal.

```text
You cannot reject, replace, or rewrite a row of the **Active tasks** part. Your
one action against a duplicate is to reject the proposal. This holds even when
the proposal is the better piece of work.
```

### Passage 50: `mc/verbs/directives/arbiter.md`, lines 179–181

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The extension exception remains. The rank and criteria rules already apply to each proposal.

```text
A proposal that starts from merged work and extends it is not a duplicate. Such
a proposal still has to win the rank against the rest of the pool, and it still
owes checkable success criteria.
```

### Passage 51: `mc/verbs/directives/arbiter.md`, lines 185–204

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Orchestration.
The shorter section preserves bounded checks, patterns, inline conditions, final selection, and the response reason.

```text
Orchestrate by default. Use read-only, depth-1 subagents for these checks:

- independent overlap checks;
- feasibility checks;
- adversarial ranking.

Select one named reference pattern:

- Fanout-And-Synthesize;
- Adversarial Verification;
- Generate-And-Filter;
- Tournament.

Execute the pattern in bounded rounds. Do not run an open-ended loop. Beyond
these patterns, you can author any orchestration when a small artifact replaces
the full working context of a sub-problem. Examples of a small artifact are a
brief, a verdict, and a ranked list. If you must hold the whole context to take
the next step, do not spawn. Keep the final rank and the final selection in
this top-level run. Execute inline, with no subagent, only for a pool that fits
in one context. State the reason in your response before the terminal action.
```

### Passage 52: `mc/verbs/directives/arbiter.md`, lines 207–209

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract.
The terminal requirement has one home.

```text
## The terminal action

Submit exactly one terminal action.
```

### Passage 53: `mc/verbs/directives/arbiter.md`, lines 210–210

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Terminal action.
The command appears once in the command surface.

```text
For a verdict, use `mc arbiter decide --run <run_id> --batch -`.
```

### Passage 54: `mc/verbs/directives/arbiter.md`, lines 211–211

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract; mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Terminal action.
The contract bounds the pool; the schema requires a concrete rejection reason.

```text
The batch covers the exact pool. Every rejection carries a concrete reason.
```

### Passage 55: `mc/verbs/directives/arbiter.md`, lines 212–219

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract.
The authority limits and completion duty apply after execution failures without an event list.

```text
Do not start another role. Do not rewrite proposal text.

Never end your turn without a terminal action. A failed orchestration is not an
exemption. You owe one call in each of these cases:

- a subagent errors;
- a provider stream disconnects during the fan-out;
- your plan cannot complete.
```

### Passage 56: `mc/verbs/directives/arbiter.md`, lines 221–221

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — A run that cannot give a result; An execution problem.
Pointer condition: an execution problem prevents a verdict.

```text
Judge the assigned proposals when you can. Report an execution problem through `mc complete --run <run_id> --infra --reason <problem>`.
```

### Passage 57: `mc/verbs/directives/arbiter.md`, lines 222–222

Category: `unneeded`.
Failure-unit accounting does not change which terminal applies.

```text
This report consumes one unit of the shared Project infrastructure allowance.
```

### Passage 58: `mc/verbs/directives/arbiter.md`, lines 223–223

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — A decision that only the operator can make.
Pointer condition: a required operator decision prevents a verdict.

```text
Report an operator question through `mc complete --run <run_id> --needs-operator --reason <question>`.
```

### Passage 59: `mc/verbs/directives/arbiter.md`, lines 224–224

Category: `unneeded`.
Failure-unit accounting does not change the report.

```text
This report consumes zero failure units
```

### Passage 60: `mc/verbs/directives/arbiter.md`, lines 224–224

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — A decision that only the operator can make.
Pointer condition: a required operator decision prevents a verdict.

```text
and stops Project proposal work until the operator resolves the question.
```

### Passage 61: `mc/verbs/directives/arbiter.md`, lines 225–225

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — A decision that only the operator can make.
The command procedure defines the affected subset.

```text
The role skill defines the affected proposal subset.
```

### Passage 62: `mc/verbs/directives/arbiter.md`, lines 225–225

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract.
The retained contract preserves proposal requirements.

```text
Preserve proposal requirements when you promote or reject a proposal.
```

### Passage 63: `mc/verbs/directives/arbiter.md`, lines 227–228

Category: `unneeded`.
Runner evidence and retry accounting do not define an Arbiter action.

```text
The runner reports complete model responses and confirms connection recovery.
Connection recovery preserves the task outcome and its retry allowance.
```

### Passage 64: `mc/verbs/directives/arbiter.md`, lines 229–229

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — An execution problem.
Pointer condition: an execution problem prevents a verdict.

```text
Do not submit connection recovery reports yourself.
```

### Passage 65: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 3–3

Category: `duplicate`.
Retained home or destination: mc/verbs/directives.go — skillPointer, Your command surface.
The generated pointer owns the read trigger, permitted-command boundary, and absence of help. Frontmatter identifies the procedure.

```text
description: The complete and only mc verb surface for the Arbiter's pool pass over proposed tasks; load before any mc call and before submitting the terminal — mc ships no --help, and a verb absent from this skill does not exist for this role.
```

### Passage 66: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 6–10

Category: `unneeded`.
The shorter title identifies the role. Profile, harness, and install explanations add no command constraint.

```text
# mc — Arbiter (pool pass)

The agent profile sets the model and effort for this run.
The same command authority applies to Codex, the Claude Agent Software Development Kit (SDK), and OpenCode.
Culler supplies the harness configuration and the skills at `~/.agents/skills/`.
```

### Passage 67: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 12–13

Category: `duplicate`.
Retained home or destination: mc/verbs/brief.go — trimArbiterBrief, Run.
The generated part supplies the placeholder mapping.

```text
`<run_id>` is the `Run identifier` fact of the **Run** part.
Use that value in each terminal action.
```

### Passage 68: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 14–15

Category: `duplicate`.
Retained home or destination: mc/verbs/brief.go — trimArbiterBrief, Proposed pool; addArbiterParts, Project.
The generated parts supply assignment and repository interpretation.

```text
The **Project** and **Proposed pool** parts define the assignment.
Read source files and project knowledge at the **Project** part's `Repository copy` path.
```

### Passage 69: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 16–17

Category: `unneeded`.
The actual pool defines the assignment. A normal proposal count and absent task subject add no action.

```text
The Strategist normally supplies two task proposals.
This proposal run has no task subject.
```

### Passage 70: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 18–19

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract.
The contract requires this run’s identity and one terminal action.

```text
`mc` refuses a terminal action from another run and applies zero changes.
A terminal action ends the run.
```

### Passage 71: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 23–23

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Terminal action.
The command fixes --batch to stdin; the payload instruction requires one JSON object.

```text
`--batch` accepts only `-`. `mc` reads the payload from stdin as strict JSON.
```

### Passage 72: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 27–35

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Terminal action.
The compact field list preserves the shape, types, rejection-only fields, and mutual exclusion.

```text
The payload is an object with one field, `verdicts`. The field `verdicts` is an array of verdict objects. Each verdict has these fields:

- The field `task` is required. Its value is the `id` of one row of the **Proposed pool** part.
- The field `decision` is required. Its value is one of `promote|reject`.
- The field `reason` is required for `reject`.
- The field `duplicate_of` is optional. It is permitted on a `reject` only. Its value is an integer task id. The section "The all-reject batch" defines its use.
- The field `rejection_category` is optional. Its only value is `invalid_criteria`, and it requires a `reject` decision.

The fields `rejection_category` and `duplicate_of` are mutually exclusive.
```

### Passage 73: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 36–36

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Terminal action.
A concrete rejection reason replaces the readable-text restatement.

```text
Each rejection reason must contain readable text.
```

### Passage 74: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 37–37

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The directive requires the invalid criterion and violated rule once.

```text
For `invalid_criteria`, the reason identifies the invalid criterion and the rule that it violates.
```

### Passage 75: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 38–40

Category: `unneeded`.
Validator internals add no requirement beyond the criteria judgment and required reason.

```text
The command checks the declaration and requires a nonblank reason.
You judge whether the criterion violates the rules.
The command does not infer that judgment from prose.
```

### Passage 76: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 42–42

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Terminal action.
The retained schema excludes extra fields and trailing data, and names the reason field.

```text
The payload permits no other key at either level. An unknown field, for example `rank`, `leverage`, or `notes`, causes `mc` to refuse the whole batch. Data after the object also causes `mc` to refuse the whole batch. Put the reasons for your judgment in the `reason` field of each verdict.
```

### Passage 77: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 44–44

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract; mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Terminal action.
The exact verdict boundary and atomic refusal rule remain at their required locations.

```text
The batch must cover the **Proposed pool** part exactly. `mc` refuses a batch that omits a row of the part as a pool mismatch. `mc` refuses a batch that names a task outside the part as a pool mismatch. `mc` applies no part of a refused batch.
```

### Passage 78: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 48–48

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Cases.
The case pointer gives the trigger.

```text
This case occurs when you reject every proposal.
```

### Passage 79: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 50–56

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — The all-reject batch.
The shorter readiness definition retains pool, status, blocked, stalled, and task-list checks.

```text
`mc` accepts the batch when the ready queue is not empty. `mc` counts a task as ready when all these conditions are true:

- the task is outside your pool;
- its status is `proposed`, `seeded`, `worked`, or `verified`;
- the task is not blocked and not stalled.

The **Active tasks** part does not show the stalled state. `mc task list` shows it in the `stalled` field.
```

### Passage 80: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 58–58

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — The all-reject batch.
The exception rule retains its empty-ready-set condition.

```text
When the ready queue is empty, each rejection requires one of these exceptions:
```

### Passage 81: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 63–64

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — The all-reject batch; Terminal action.
Mutual exclusion already limits each rejection to one exception.

```text
The batch can combine these exceptions across proposals.
Each rejection uses only one exception.
```

### Passage 82: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 67–70

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — The all-reject batch.
The reference rule retains prior active or merged work in this Project and excludes the pool.

```text
Each `duplicate_of` reference names prior work that the proposal duplicates.
The reference must name a row of the **Active tasks** part or a merged task of this Project.
`mc task list --all` shows merged tasks with `archived` set and `decision` equal to `approved`.
Do not cite a member of your own pool.
```

### Passage 83: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 71–71

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The superset rule applies to every pool comparison.

```text
When two valid pool proposals overlap, promote the superset.
```

### Passage 84: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 73–73

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — The all-reject batch.
The retained rule requires an overlap reason and distinguishes reference checks from judgment.

```text
`mc` checks only that the cited row is existing work of this Project. The overlap itself is your judgment. State the overlap in `reason`.
```

### Passage 85: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 75–91

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
Criteria validity and capability limits have one home in routine review.

```text
The `invalid_criteria` category applies when acceptance criteria violate the proposal rules in the role directive.
Examples include these invalid criteria:

- A whole-suite success requirement, such as `./mc/check.sh passes`.
- Behavior outside the task's scope.
- A required check that needs an unavailable capability.

An available suite command does not establish that its tests can run in the container.
Required capabilities exclude these resources:

- Access to the operator's host.
- A Docker daemon.
- macOS system interfaces.
- Additional container privileges.
- A hosted continuous integration service.

New or changed tests must pass on the submitted commit.
```

### Passage 86: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 92–92

Category: `unneeded`.
The Verifier’s duties do not define an Arbiter action.

```text
Broad regression checks belong to the Verifier's duties for every task.
```

### Passage 87: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 93–93

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Proposal review.
The unchanged-failure rule remains with the other criteria rules.

```text
An unchanged Project test failure alone permits a proposal that satisfies these rules.
```

### Passage 88: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 95–96

Category: `unneeded`.
Rejection archival and later dispatch mechanics do not affect the decision.

```text
An accepted rejection archives the proposal and retains its reason in `task.rejected` activity.
A later Strategist run proposes new work under the normal dispatch rules.
```

### Passage 89: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 97–98

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — The all-reject batch.
The procedure permits qualified exceptions and requests an operator decision only when its rules prevent a verdict.

```text
Invalid criteria alone require no operator decision when each rejection qualifies for an exception.
Other decisions that only the operator can make use the procedure below.
```

### Passage 90: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 102–102

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract; mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — A run that cannot give a result.
The contract owns terminal count; the procedure requires a verdict when possible.

```text
Two events end a run without a result. The event selects the command. Each command in this section ends your run, so submit exactly one. No command here substitutes for a judgment of the pool.
```

### Passage 91: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 106–106

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — A decision that only the operator can make.
The report preserves proposals while the operator resolves the question.

```text
The report records one shared Project question. Each affected proposal receives Decision Pending and retains Proposed.
```

### Passage 92: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 111–111

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — A decision that only the operator can make.
The retained text defines the affected subset.

```text
The first command includes every assigned proposal. The second command identifies the affected subset of your assignment.
```

### Passage 93: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 112–112

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — A decision that only the operator can make.
The operator resolution still precedes resumed proposal work.

```text
The operator resolves the shared decision before proposal work resumes.
```

### Passage 94: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 112–112

Category: `unneeded`.
Failure-unit accounting does not change the report.

```text
The report consumes zero failure units.
```

### Passage 95: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 119–122

Category: `unneeded`.
Project allowance and dispatch accounting do not change the failure report.

```text
Your proposal run belongs to the Project, with no task subject.
Dispatch, launch, Strategist proposal creation, and Arbiter proposal review share one Project infrastructure allowance of three counted failures.
The third counted failure stops each new agent run with the Project lease in the Project.
A successful terminal result restores that Project allowance. An operator decision report preserves its count.
```

### Passage 96: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 123–123

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — An execution problem.
The retained instruction requires direct observations rather than an inferred connection diagnosis.

```text
An agent diagnosis supplies an unknown cause. Culler requires direct runner or resident evidence for shared connection conditions.
```

### Passage 97: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 127–131

Category: `unneeded`.
Historical selection counts and order do not change scope judgments.

```text
The **Rejected work** and **Cancelled work** parts contain at most 20 historical tasks total from this Project.
Culler selects up to 10 cancelled tasks first.
Rejected tasks fill the unused places.
Each part lists the newest decisions first, with larger task numbers first for equal decision times.
An empty part is absent.
```

### Passage 98: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 132–132

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Historical work.
Pointer condition: Rejected work or Cancelled work is present.

```text
Older tasks remain available through the permitted read commands.
```

### Passage 99: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 134–141

Category: `unneeded`.
The records expose their fields; format limits require no separate action.

```text
Each historical entry contains these fields:

- The task number.
- The recorded title.
- The decision.

A cancelled entry also contains a recorded reason excerpt when available, with at most 160 Unicode characters.
Historical entries exclude descriptions.
```

### Passage 100: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 142–142

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Reads.
The task read supplies absent scope.

```text
Use `mc task get <id>` when a historical title leaves the scope unclear.
```

### Passage 101: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 143–143

Category: `unneeded`.
The full descriptions remain visible in the records.

```text
The **Active tasks** and **Proposed pool** parts retain full descriptions for comparison of scope and success criteria.
```

### Passage 102: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 145–147

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Historical work.
Pointer condition: Rejected work or Cancelled work is present. The directive already requires scope judgments rather than titles alone.

```text
Compare each proposal with both historical parts.
Treat equivalent work as a reason to reject the proposal under the permitted batch rules, even with a different title.
Use each cancellation reason as context for the operator's preference.
```

### Passage 103: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 148–149

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — The all-reject batch.
The single reference rule excludes historical tasks; the same procedure owns the operator fallback.

```text
Historical tasks do not qualify as existing work for `duplicate_of`.
Use the operator decision procedure when the all-reject rules prevent a permitted verdict.
```

### Passage 104: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 158–158

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Reads; mc/verbs/directives/arbiter.md — Proposal review.
The list limitation, task-read condition, and overlap sources remain without repeated explanations.

```text
`mc task list` returns no `description`. It therefore cannot show you what a row touches. You find overlap in what a row touches. The **Active tasks** part of the run brief holds the full description of each active task. Judge overlap against that part. Use `mc task get <id>` only for a row that neither the **Proposed pool** part nor the **Active tasks** part names.
```

### Passage 105: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 160–169

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract; mc/verbs/directives.go — skillPointer, Your command surface.
The permitted-command boundary excludes these writes and prevents searches for extra commands.

```text
## Out of scope

These operations belong to the operator and to Culler:

- add a task;
- unblock a task;
- interrupt a task;
- start a run.

A search for a command that this skill does not list wastes the run.
```

### Passage 106: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 171–173

Category: `unneeded`.
Unpermitted runner command names and response-record mechanics do not help the Arbiter.

```text
The runner owns `mc run model-response` and `mc collie model-response`.
These commands record complete model responses for the current attempt.
Do not call these runner commands.
```

### Passage 107: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 174–174

Category: `moved behind a pointer`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — An execution problem.
Pointer condition: an execution problem prevents a verdict.

```text
Continue the assigned work after connection recovery.
```

### Passage 108: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 178–180

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Project role customization.
The directive owns operator precedence and the contract boundary.

```text
The Culler contract has priority over custom instructions.
Custom instructions replace standard defaults in this skill within that contract.
Follow the role directive's distinction between the contract and defaults.
```

### Passage 109: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 181–181

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Project role customization.
The directive's conflict pointer selects this procedure; the skill requires the terminal that ends the run.

```text
If a custom instruction conflicts with the contract during work, submit this terminal:
```

### Passage 110: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 185–190

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Terminal action; Project role customization.
JSON is defined on first use; the compact conflict fields preserve the request shape.

```text
The JavaScript Object Notation (JSON) object contains these fields:

- The `run` field: your run identifier.
- The `revision` field: the customization revision from the run brief.
- The `passage` field: the exact conflicting text from that revision.
- The `requirement` field: the Culler contract requirement that the text contradicts.
```

### Passage 111: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 193–193

Category: `unneeded`.
Internal record selection requires no Arbiter action.

```text
The command records the Project, role, and subject from your run.
```

### Passage 112: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, line 194, first clause

Category: `duplicate`.
Retained home or destination: mc/verbs/skills/arbiter/mc-arbiter/SKILL.md — Project role customization.
The terminal instruction states that the command ends the run with a customization conflict.

```text
It ends the run with a customization conflict
```

### Passage 113: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, line 194, subject clause

Category: `unneeded`.
The Arbiter has no task subject whose stage it must preserve.

```text
and preserves the subject's stage
```

### Passage 114: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, line 194, accepted-work clause

Category: `duplicate`.
Retained home or destination: mc/verbs/directives/arbiter.md — Culler contract.
The contract requires the Arbiter to preserve accepted work.

```text
and accepted work.
```

### Passage 115: `mc/verbs/skills/arbiter/mc-arbiter/SKILL.md`, lines 195–197

Category: `unneeded`.
Internal role blocks, pause state, and allowance accounting require no Arbiter action.

```text
A run without a subject blocks that role within its Project.
The Project retains its pause state.
The command preserves all failure and correction allowances.
```

## Validation

`./mc/check.sh` exits 1 in this container.
Its only remaining failure is `mc/boundary.TestValidateHomeAcceptsTheRealOperatorHome`.
The container mounts `/home/agent` as a filesystem root; the actual operator home is unavailable.
The assigned base reproduces the same failure with the same message.
The boundary package, its dependencies, and module declarations remain unchanged.
The comparison uses the base test through a Go overlay, the same home, and `-count=1`.
This host test remains unverified; the full check does not pass here.

All new and changed tests pass, including the complete `mc/verbs` package.
The gate also passes formatting, each vet configuration, portable deployment tests, and the shell contract tests.
The untagged filesystem guard command runs separately because the failed full suite stops the script before that step.

| Check | Result |
| --- | --- |
| `./mc/check.sh` | Exit 1; only the unchanged operator-home failure above. |
| New and changed input tests through `mc/test.sh -count=1 ./verbs/ -run ...` | Exit 0; the conflict terminal and promotion contract assertions pass. |
| Untagged filesystem guard group through `mc/test.sh -count=1 -tags=` | Exit 0; two tests remain unverified on the overlay filesystem. |
| `mise exec -- bun test skills.test.ts` in `runner/agent-runner` | Exit 0; 16 tests pass. |
| Other-role output comparison against the base generators | Exit 0 for both runs; five pipeline roles have identical brief and skill hashes. |
| Command-form comparison with the base skill | All 12 command forms remain identical. |
| `git diff --check` | Exit 0. |

The output comparison normalizes only environment identifier values from its temporary database.
It compares complete rendered briefs and delivered skill bodies for Strategist, Scribe, Worker, Verifier, and Packager.
The comparison harness and both logs remain in the task artifacts.

The release source directories exclude group and other write access, as the deployment guard requires.
The new assertions fail against commit `780357aa10f35224f4aea10283d250d74b9f976d` for both missing rules.
It passes with the promotion limit in the contract and the conflict terminal in the skill.
The test also checks that the all-reject procedure retains its operator decision fallback.

The task changes no dashboard behavior or deployment behavior.
The browser, Docker, and nightly lanes do not run for this instruction change.
The macOS tests require a different platform and remain unverified below.
Shared skills and other roles' input files remain byte-identical to the base.
The wiki remains unchanged.

### Unverified checks

The following entries preserve each skipped check and the unavailable operator-home check.
The failed full-suite wrapper prints raw skips, so this report names them in the repository's `UNVERIFIED` form.

```text
UNVERIFIED mc/boundary TestBroadRootRejectsFirmlinkAliasRoutes: firmlinks are macOS-specific
UNVERIFIED mc/boundary TestValidateHomeRejectsNonSlashFilesystemRoot: the repository's measured non-/ volume witness is macOS-specific
UNVERIFIED mc/boundary TestOwnControlAncestorExemptionIsExactAndAssociated/same-file_different-spelling_workspace_permits: case-sensitive volume: no same-file/different-spelling pair available
UNVERIFIED mc/cmd/mc TestCullerLogsSelectsHomeAndRetainsTail: The public log command requires the Mac host.
UNVERIFIED mc/cmd/mc TestCullerReleaseDeadlineCannotReportSuccess: The public release check requires the Mac host.
UNVERIFIED mc/cmd/mc TestCullerUninstallModesAndPreview: The public uninstall command requires the Mac host.
UNVERIFIED mc/cmd/mc TestUninstallRejectsInstallationPublishedBeforeLocks: The startup inventory requires macOS.
UNVERIFIED mc/substrate TestOpenRefusesASpineOutsideTheLockDomain: This test requires a test directory on a block-device filesystem: refusing to open the spine under /tmp/TestOpenRefusesASpineOutsideTheLockDomain1190843320/001: its mount / is fstype=overlay source=overlay, which is not a block-device-backed local filesystem — the spine must live on a runtime-local named volume, because a shared host path splits its locks across two kernels and corrupts it
UNVERIFIED mc/substrate TestOpenReadOnlyRefusesASpineOutsideTheLockDomain: This test requires a test directory on a block-device filesystem: refusing to open the spine under /tmp/TestOpenReadOnlyRefusesASpineOutsideTheLockDomain2991588891/001: its mount / is fstype=overlay source=overlay, which is not a block-device-backed local filesystem — the spine must live on a runtime-local named volume, because a shared host path splits its locks across two kernels and corrupts it
UNVERIFIED mc/boundary TestValidateHomeAcceptsTheRealOperatorHome: the actual operator home is unavailable; /home/agent is a container mount root. The test fails on the assigned base and final tree.
```

The following tests compile only on macOS.

```text
UNVERIFIED mc/boundary/acl_membership_darwin_test.go TestDarwinACLPrincipalResolverRequiresOwnerUserIdentity: requires macOS; this run uses Linux arm64
UNVERIFIED mc/boundary/acl_membership_darwin_test.go TestDarwinACLInspectorWiresMembershipResolver: requires macOS; this run uses Linux arm64
UNVERIFIED mc/boundary/acl_native_darwin_test.go TestTrustPolicyFileReadsNativeDarwinACL: requires macOS; this run uses Linux arm64
UNVERIFIED mc/boundary/acl_native_darwin_test.go TestTrustHomeDirReadsNativeDarwinACL: requires macOS; this run uses Linux arm64
UNVERIFIED mc/boundary/acl_native_darwin_test.go TestOperatorHomeDoesNotAcquireMCHomeACLSeam: requires macOS; this run uses Linux arm64
UNVERIFIED mc/boundary/volume_case_darwin_test.go TestSuffixCaseModeForPathReportsKnownDarwinVolume: requires macOS; this run uses Linux arm64
UNVERIFIED mc/boundary/volume_case_darwin_test.go TestSuffixCaseModeForPathFailsClosedOnDarwinLookupError: requires macOS; this run uses Linux arm64
UNVERIFIED mc/cmd/mc/attachment_canary_acl_darwin_test.go TestAttachmentCanaryScratchRefusesGrantingNativeACL: requires macOS; this run uses Linux arm64
UNVERIFIED mc/cmd/mc/attachment_scope_darwin_test.go TestPrivateAttachmentSpineRejectsProductionHostScope: requires macOS; this run uses Linux arm64
UNVERIFIED mc/cmd/mc/setup_install_prod_darwin_test.go TestSetupExecutablePublicationIsDeferredAndAtomic: requires macOS; this run uses Linux arm64
UNVERIFIED mc/cmd/mc/setup_install_prod_darwin_test.go TestOnboardCLIRequiresPrivateSourceDeployment: requires macOS; this run uses Linux arm64
UNVERIFIED mc/cmd/mc/uninstall_agentsview_darwin_test.go TestUninstallFindsTheHomeOfALoneAgentsViewUnit: requires macOS; this run uses Linux arm64
UNVERIFIED mc/deployment/agentprofile_auth_darwin_test.go TestAuthorizationExecutableOutsideSupervisedPath: requires macOS; this run uses Linux arm64
UNVERIFIED mc/deployment/cli_install_darwin_test.go TestCLIPathResolvesInActualNewMacShells: requires macOS; this run uses Linux arm64
UNVERIFIED mc/deployment/host_secret_darwin_test.go TestHostSecretKeychainRoundTripUsesIsolatedStoreAndHandlesLongValues: requires macOS; this run uses Linux arm64
UNVERIFIED mc/deployment/runtimeauth_keychain_darwin_test.go TestAcquireRuntimeAuthExportsClaudeCredentialsFromItsIsolatedKeychain: requires macOS; this run uses Linux arm64
UNVERIFIED mc/deployment/runtimeauth_keychain_darwin_test.go TestAcquireRuntimeAuthRefusesAmbiguousKeychainCandidates: requires macOS; this run uses Linux arm64
UNVERIFIED mc/deployment/runtimeauth_keychain_darwin_test.go TestAcquireRuntimeAuthRefusesWhenTheIsolatedKeychainStayedEmpty: requires macOS; this run uses Linux arm64
UNVERIFIED mc/deployment/runtimeauth_keychain_darwin_test.go TestClaudeCredentialStoreNeverFallsBackToAnotherKeychain: requires macOS; this run uses Linux arm64
UNVERIFIED mc/deployment/runtimeauth_keychain_darwin_test.go TestAcquireRuntimeAuthPreparesTheKeychainBeforeSpendingALogin: requires macOS; this run uses Linux arm64
UNVERIFIED mc/deployment/supervision_plist_darwin_test.go TestSupervisionPropertyListsPassPlatformValidation: requires macOS; this run uses Linux arm64
UNVERIFIED mc/onboarding/session_acl_darwin_test.go TestSessionRejectsNativeAccessGrants: requires macOS; this run uses Linux arm64
UNVERIFIED mc/onboarding/session_acl_darwin_test.go TestSessionRejectsInheritedAccessGrantBeforePublication: requires macOS; this run uses Linux arm64
UNVERIFIED mc/onboarding/terminal_pty_darwin_test.go TestReleaseTerminalInput: requires macOS; this run uses Linux arm64
```
