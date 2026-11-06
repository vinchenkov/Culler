# Strategist instruction audit

Base commit: `da32b26c8ae11b84f40d6a5c4d95c7154d8d9ed3`.

The audit covers the Strategist directive, its command skill, and its generated brief explanations and skill pointers.
Shared skill contents are outside this audit. Their files and references remain unchanged.
Other roles retain their directives, skills, and generated output.

The reference is [Matt Pocock’s writing-for-agents](https://github.com/mattpocock/skills/tree/main/skills/productivity/writing-for-agents).
Its information hierarchy, conditional pointers, and single-source rules guide this revision.
The existing writing skill remains the language standard.

## Word counts

Both counts use Python `len(re.findall(r"\S+", text))` on the complete UTF-8 file.
The method includes headings, frontmatter, tables, and command forms.
The before text comes from the base commit. The after text comes from the files delivered with this report.

| File | Before | After |
| --- | ---: | ---: |
| `mc/verbs/directives/strategist.md` | 1603 | 420 |
| `mc/verbs/skills/strategist/mc-strategist/SKILL.md` | 1148 | 435 |

The command skill is the only Strategist-specific skill. This task adds or removes no skill.

## Reasons for retained sections

| File | Section | Concrete reason |
| --- | --- | --- |
| Directive | Title | Identifies the assigned role. |
| Directive | Culler contract | Limits proposals to this Project, preserves accepted work and evidence, and defines authority, safety, and terminal requirements. |
| Directive | Proposal requirements | Requires one-commit work, scoped checks, available evidence, passing changed tests, and duplicate checks. |
| Directive | Project role customization | Distinguishes operator defaults from contract requirements and directs conflicts to their procedure. |
| Directive | Research | Selects useful work, controls research and delegation, and requires the inline reason outside the payload. |
| Directive | Completion | Selects the procedures for execution problems and required operator decisions. |
| Command skill | Frontmatter and title | Identifies the installed skill and its command, dependency, and exceptional-completion triggers. |
| Command skill | Proposal batch | Defines batch size, the empty result, the request schema, transaction behavior, and retry after a rejection. |
| Command skill | Reads | Supplies permitted reads and the scope lookup when a task title is insufficient. |
| Command skill | Research dependencies | Requires compatibility checks before optional dependency use and preserves the shared preparation procedure. |
| Command skill | Execution problems and operator decisions | Distinguishes recoverable work, execution failure, connection reports, and a required operator decision. |
| Command skill | Project role customization | Supplies the conflict terminal and the exact evidence fields that it requires. |

The directive keeps rules needed to assess proposals in every run.
Conditional dependency, failure, decision, and conflict procedures stay in the command skill.
The generated pointer resolves to `~/.agents/skills/mc-strategist/SKILL.md` inside the agent container.
It requires a read before each command, including the terminal.
The directive also names each condition that requires a procedure before command use.

## Authority and delivery

The proposal command, command allowlist, request decoder, and terminal handlers remain unchanged.
The Arbiter retains proposal selection. The operator retains proposal resolutions, role customization, and required decisions.
The skill preserves two proposals, fewer valid proposals, and an empty batch as separate permitted results.
The directive preserves scope, duplicate checks, container checks, and the prohibition on whole-suite acceptance criteria.
Examples no longer name Culler's own check command or a particular hosted service.

`trimStrategistBrief` changes only the Strategist's explanations and removes its credential and failure-mechanics parts.
It preserves the assignment facts, rows, text, and conditional presence of the retained parts.
`skillPointer` selects the shorter command pointer only for `mc-strategist`.
The shared skill descriptions and paths keep their previous text and meaning.

A comparison captured generated inputs from the base and final implementations under the same fixture.
The fixture included an active task, both history parts, an operator resolution, and Vision content.
The Arbiter, Worker, Verifier, Packager, and Scribe brief and skill bytes matched.
The Collie and Setup Agent skill sets and generated pointers also matched.
The task artifact retains both captures, the capture test, and their comparison hashes.

## Passage inventory

Each entry quotes the original text from the base commit and assigns one required category.
The inventory includes deleted wording, moved instructions, and rewritten passages.
For a rewritten passage, `unneeded` refers to the removed formulation; its note identifies behavior that remains.
A `duplicate` entry names the single retained file and section.
A `moved behind a pointer` entry names its destination and trigger.
Unchanged lines need no entry. Removed headings have no independent behavior.

## Original passages from `mc/verbs/directives/strategist.md`

### Passage 1: original lines 3–6

Category: `unneeded`. The role cannot change its model, harness, provider proxy, or host credential renewal.

```text
The agent profile sets the model and effort for this run.
Culler selects Codex, the Claude Agent Software Development Kit (SDK), or OpenCode as the harness.
The role contract and the installed skills apply to each harness.
OpenCode web search is available only when the selected provider supports it.
```

### Passage 2: original lines 8–10

Category: `unneeded`. The role cannot change its model, harness, provider proxy, or host credential renewal.

```text
Culler keeps provider credentials on the host.
This container uses a temporary Culler token through the host provider proxy.
The host renews account access without a credential update inside the container.
```

### Passage 3: original lines 11–11

Category: `unneeded`. The proxy-specific formulation is unnecessary. `mc/verbs/directives/strategist.md`, **Culler contract** retains configuration preservation and token exclusion.

```text
Preserve the proxy configuration and exclude Culler tokens from output.
```

### Passage 4: original lines 13–14

Category: `unneeded`. The cross-role environment tour changes no proposal decision. The Project part supplies the committed repository path.

```text
Setup and pipeline agents share container permissions, resource limits, network policy, and workspace paths.
Role authority and credentials remain separate.
```

### Passage 5: original lines 16–18

Category: `unneeded`. The cross-role environment tour changes no proposal decision. The Project part supplies the committed repository path.

```text
Culler preserves the Project home at `HOME` across Setup Agent runs and pipeline runs.
The home retains dependency caches, installed tools, and Project tool configuration.
This run receives a fresh committed projection and separate credentials, sessions, role skills, and instructions.
```

### Passage 6: original lines 19–20

Category: `moved behind a pointer`. Destination: `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Research dependencies**. Trigger: before research uses Project dependencies.

```text
This role omits automatic dependency preparation.
Check retained dependencies against the current declarations and the selected image before use.
```

### Passage 7: original lines 24–24

Category: `duplicate`. Retained at `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch** for batch size and `mc/verbs/directives/strategist.md`, **Culler contract** for evidence and Arbiter selection.

```text
Propose two tasks within the Project vision. Preserve proposal limits and leave selection to the Arbiter.
```

### Passage 8: original lines 26–27

Category: `unneeded`. The introduction adds no rule beyond the contract that follows.

```text
The contract retains the pipeline topology and this role's responsibility.
The contract also retains these requirements:
```

### Passage 9: original lines 29–35

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Culler contract** and `mc/verbs/directives/strategist.md`, **Proposal requirements**. These consolidate the repeated safety, evidence, deliverable, authority, and terminal rules.

```text
- Preserve the acceptance criteria and accepted work.
- Report evidence and results truthfully.
- Produce the required deliverables.
- Use `mc`, the Mission Control command, for Culler state changes.
- Use only permitted commands with your own run identity.
- Preserve repository, artifact, and display safety constraints.
- Submit exactly one permitted terminal action to end the run.
```

### Passage 10: original lines 39–39

Category: `duplicate`. Retained in `mc/verbs/brief.go`, the Strategist **Project role customization** explanation.

```text
The **Project role customization** part contains the operator's instructions for this Project and role.
```

### Passage 11: original lines 40–42

Category: `unneeded`. The separate default statements become one concise rule in `mc/verbs/directives/strategist.md`, **Project role customization**.

```text
Follow them within your Culler contract.
They replace standard defaults where those defaults differ.
Custom instructions also replace defaults in the run brief and role skills.
```

### Passage 12: original lines 45–46

Category: `moved behind a pointer`. Destination: `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Project role customization**. Trigger: a custom instruction conflicts with the contract.

```text
If an instruction conflicts with your contract, report a customization conflict and explain the conflict.
The command skill describes the `mc run customization-conflict` terminal.
```

### Passage 13: original lines 47–48

Category: `unneeded`. Pause counters and revision-zero interpretation require no action. The brief includes the actual custom instructions.

```text
This command preserves the Project pause state and all failure and correction allowances.
Revision zero means that the run received no custom instructions.
```

### Passage 14: original lines 50–50

Category: `unneeded`. The pipeline tour and role introduction are unnecessary. `mc/verbs/directives/strategist.md`, **Culler contract** retains proposal responsibility and Arbiter authority.

```text
Culler runs a pipeline of agent roles on one Project, a Git repository, for one human operator. The Strategist proposes tasks. The Arbiter selects them. The Worker builds each task. The Verifier judges the result. The Packager writes the review packet that the operator approves or rejects. Each role runs in a fresh container and knows only this run brief and the skills that it names. `mc` is the command-line program of Culler. A role reports its result with one `mc` command, the terminal action, and that command ends the run.
```

### Passage 15: original lines 52–52

Category: `unneeded`. The pipeline tour and role introduction are unnecessary. `mc/verbs/directives/strategist.md`, **Culler contract** retains proposal responsibility and Arbiter authority.

```text
You are the Strategist. You find fresh, high-leverage work in this run's Project and in no other Project. No role supplies your input: you read the run brief and the Project. You file each piece of work as a proposal. The Arbiter receives your proposals. It promotes a proposal to accept it as work for the pipeline, or it rejects the proposal.
```

### Passage 16: original lines 54–54

Category: `unneeded`. The proposal, pool, and destination glossary is unnecessary. The directive uses proposal and existing-work rules directly.

```text
These terms apply in this run:
```

### Passage 17: original lines 56–58

Category: `unneeded`. The proposal, pool, and destination glossary is unnecessary. The directive uses proposal and existing-work rules directly.

```text
- A proposal is a candidate task.
- The pool is the set of proposals of this Project that the Arbiter did not judge yet.
- An active task is a promoted task whose work is absent from the destination branch. The destination branch receives each approved change.
```

### Passage 18: original lines 60–60

Category: `unneeded`. The brief already presents each part with a heading and explanation.

```text
## The run brief
```

### Passage 19: original lines 62–62

Category: `unneeded`. The brief already presents each part with a heading and explanation.

```text
The parts of the run brief follow this directive. A part is a section with a heading, an explanation, and data. A part that is absent does not apply to this run. You can receive these parts:
```

### Passage 20: original lines 64–64

Category: `duplicate`. Retained in `mc/verbs/brief.go`, **Run**.

```text
- The **Run** part holds the run identifier. It is the `<run_id>` value of each `mc` command.
```

### Passage 21: original lines 65–65

Category: `duplicate`. Retained in `mc/verbs/brief.go`, **Proposal decisions**.

```text
- The **Proposal decisions** part holds the operator resolutions for Project proposal questions. Apply each relevant resolution.
```

### Passage 22: original lines 66–66

Category: `duplicate`. Retained in `mc/verbs/brief.go`, **Project**.

```text
- The **Project** part names the Project. Its `Repository copy` fact is the path of a read-only copy of the committed files of the Project.
```

### Passage 23: original lines 67–67

Category: `duplicate`. Retained in `mc/verbs/brief.go`, **Vision**, and `mc/verbs/directives/strategist.md`, **Culler contract**.

```text
- The **Vision** part holds the goal of the Project, the content of its `VISION.md` file.
```

### Passage 24: original lines 68–68

Category: `duplicate`. Retained in `mc/verbs/brief.go`, **Active tasks**; the unused pool definition is omitted.

```text
- The **Active tasks** part holds each proposal and each active task of this Project, by title. A row with the status `proposed` is a member of the pool.
```

### Passage 25: original lines 69–70

Category: `duplicate`. Retained in `mc/verbs/brief.go`, **Rejected work** and **Cancelled work**.

```text
- The **Rejected work** part holds compact entries for recent rejected tasks of this Project.
- The **Cancelled work** part holds compact entries for recent cancelled tasks of this Project.
```

### Passage 26: original lines 72–76

Category: `unneeded`. Exact history quotas, tie order, and omission rules require no Strategist action. Existing tests still verify the delivered rows.

```text
The historical parts contain at most 20 tasks total.
Culler selects up to 10 cancelled tasks first.
Rejected tasks fill the unused places.
Each part lists the newest decisions first, with larger task numbers first for equal decision times.
An empty part is absent.
```

### Passage 27: original lines 77–77

Category: `moved behind a pointer`. Destination: `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Reads**. Trigger: a historical title needs a scope lookup; read the skill before a command.

```text
Older tasks remain available through the permitted read commands.
```

### Passage 28: original lines 79–79

Category: `unneeded`. The rows label their fields. The lookup procedure covers missing scope; exact excerpt lengths need no agent action.

```text
Each historical entry contains these fields:
```

### Passage 29: original lines 81–83

Category: `unneeded`. The rows label their fields. The lookup procedure covers missing scope; exact excerpt lengths need no agent action.

```text
- The task number.
- The recorded title.
- The decision.
```

### Passage 30: original lines 85–86

Category: `unneeded`. The rows label their fields. The lookup procedure covers missing scope; exact excerpt lengths need no agent action.

```text
A cancelled entry also contains a recorded reason excerpt when available, with at most 160 Unicode characters.
Historical entries exclude descriptions.
```

### Passage 31: original lines 87–87

Category: `moved behind a pointer`. Destination: `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Reads**. Trigger: an active or historical title leaves the scope unclear; read before the lookup command.

```text
Use `mc task get <id>` when a historical title leaves the scope unclear.
```

### Passage 32: original lines 88–88

Category: `unneeded`. The active rows already label their fields.

```text
The **Active tasks** part retains task numbers, titles, and stages.
```

### Passage 33: original lines 90–90

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Culler contract**: proposals use repository evidence and the accepted Vision.

```text
## The goal
```

### Passage 34: original lines 92–93

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Culler contract**: proposals use repository evidence and the accepted Vision.

```text
- Each proposal must advance the goal of the Project.
- Base each proposal on real signal from the repository copy and from the goal.
```

### Passage 35: original lines 95–95

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements**: one rule excludes equivalent repository, active, rejected, and cancelled work.

```text
## Work that a row already covers
```

### Passage 36: original lines 97–98

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements**: one rule excludes equivalent repository, active, rejected, and cancelled work.

```text
Do not propose work that the **Active tasks**, **Rejected work**, or **Cancelled work** part covers.
A different title for equivalent work still counts as repeated work.
```

### Passage 37: original lines 99–99

Category: `duplicate`. Retained in `mc/verbs/brief.go`, **Cancelled work**.

```text
Use each cancellation reason as context for the operator's preference.
```

### Passage 38: original lines 101–101

Category: `duplicate`. Retained in `mc/verbs/brief.go`, **Project** and **Active tasks**, which distinguish committed files from pending work.

```text
The repository copy shows merged work.
```

### Passage 39: original lines 103–103

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements**. The Culler-specific command example is omitted.

```text
## Proposals
```

### Passage 40: original lines 105–111

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements**. The Culler-specific command example is omitted.

```text
- Each task delivers one Git commit to the Project repository. Work whose result is not a commit is not a task.
- Every description must state checkable success criteria.
- Each criterion must describe behavior within the task's scope.
- Each criterion must name a check that the Worker and the Verifier can run in an agent container.
- New or changed tests must pass on the submitted commit.
- Do not require whole-suite success, such as `./mc/check.sh passes`, as an acceptance criterion.
- Check the capabilities of the required tests. An available suite command does not prove that its tests can run.
```

### Passage 41: original lines 112–112

Category: `unneeded`. The Strategist does not execute the Verifier duties.

```text
- Broad regression checks belong to the Verifier's duties for every task.
```

### Passage 42: original lines 113–118

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements** as capability limits. The provider-specific service example is omitted.

```text
- No agent container has these capabilities:
  - host access;
  - a Docker daemon;
  - macOS system interfaces;
  - additional container privileges;
  - a run on a hosted continuous integration (CI) service, such as GitHub Actions.
```

### Passage 43: original lines 119–119

Category: `duplicate`. Retained at `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**.

```text
- Propose two tasks in one batch. Submit fewer only when fewer valid tasks satisfy the Project vision.
```

### Passage 44: original lines 120–121

Category: `unneeded`. The expanded formulation becomes two direct research rules in `mc/verbs/directives/strategist.md`, **Research**. Internet research remains available to read-only subagents.

```text
- Select the candidates with the best reward for the cost. Discard the other candidates before you file.
- You and your read-only subagents can do research on the internet. Use it when it helps you find work that advances the goal.
```

### Passage 45: original lines 122–122

Category: `duplicate`. Retained at `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**, in the `project` field constraint.

```text
- The pool of this Project receives every proposal. `mc` refuses a proposal for another Project.
```

### Passage 46: original lines 123–123

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Culler contract**, with the other authority limits.

```text
- Do not rank your own proposals. Do not start the work of a proposal.
```

### Passage 47: original lines 125–126

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements**, now inside the Culler contract section.

```text
These criteria rules belong to the Culler contract.
An unchanged Project test failure alone permits a proposal that satisfies these rules.
```

### Passage 48: original lines 128–128

Category: `unneeded`. The expanded workflow instructions become the concise delegation rules in `mc/verbs/directives/strategist.md`, **Research**. The same patterns, context limit, and inline reason remain.

```text
## Orchestration
```

### Passage 49: original lines 130–130

Category: `unneeded`. The expanded workflow instructions become the concise delegation rules in `mc/verbs/directives/strategist.md`, **Research**. The same patterns, context limit, and inline reason remain.

```text
Orchestrate by default:
```

### Passage 50: original lines 132–142

Category: `unneeded`. The expanded workflow instructions become the concise delegation rules in `mc/verbs/directives/strategist.md`, **Research**. The same patterns, context limit, and inline reason remain.

```text
- First, identify the research branches that a subagent can do. A subagent can do a branch when a small artifact can replace the full working context of the branch. A small artifact is a brief, a verdict, or a ranked list.
- Fan the branches out to read-only, depth-1 subagents. Each subagent reads the Project of this run only.
- Author the fan-out as a dynamic workflow. The deterministic script is the plan. Fix the plan before it runs.
- Select from these reference patterns:
    - Fanout-And-Synthesize;
    - Adversarial Verification;
    - Generate-And-Filter;
    - Tournament.
- You can author a different orchestration for a branch that meets the same condition.
- Do not spawn when the next step needs the full working context of the branch.
- Execute inline, with no subagent, only when the whole research fits in a single context. State the reason in your response before the terminal action. Do not state the reason in the batch payload.
```

### Passage 51: original lines 144–144

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Research**: read-only, depth-1, Project-only subagents make no `mc` calls.

```text
Subagents have these limits:
```

### Passage 52: original lines 146–148

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Research**: read-only, depth-1, Project-only subagents make no `mc` calls.

```text
- Do not let a subagent write artifacts.
- Do not let a subagent invoke `mc`.
- Do not let a subagent spawn successors.
```

### Passage 53: original lines 150–150

Category: `duplicate`. The terminal requirement remains at `mc/verbs/directives/strategist.md`, **Culler contract**. The **Completion** pointer selects `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Execution problems and operator decisions**.

```text
## Terminal action
```

### Passage 54: original lines 152–152

Category: `duplicate`. The terminal requirement remains at `mc/verbs/directives/strategist.md`, **Culler contract**. The **Completion** pointer selects `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Execution problems and operator decisions**.

```text
Submit exactly one terminal action. The role skill defines the execution problem and operator decision terminals.
```

### Passage 55: original lines 154–154

Category: `duplicate`. Retained at `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**.

```text
A proposal batch creates every proposal and ends the run in one transaction.
```

### Passage 56: original lines 155–155

Category: `unneeded`. The Strategist cannot control dispatch, launch, or the shared infrastructure allowance.

```text
Dispatch and launch share the Project infrastructure allowance with Strategist proposal creation and Arbiter proposal review.
```

### Passage 57: original lines 157–157

Category: `duplicate`. Retained at `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch** as the sole proposal command form.

```text
- `mc strategist propose --run <run_id> --batch -`
```

### Passage 58: original lines 159–159

Category: `duplicate`. Retained at `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch** for the full batch and `mc/verbs/directives/strategist.md`, **Culler contract** for the prohibition on starting runs.

```text
The successful proposal terminal carries the full proposal batch. Do not start another role.
```

### Passage 59: original lines 161–162

Category: `moved behind a pointer`. Destination: `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Execution problems and operator decisions**. Trigger: an execution problem or required operator decision. `mc/verbs/directives/strategist.md`, **Culler contract** retains exactly one terminal after failure.

```text
Never end your turn without a terminal action. A failed orchestration is not an exemption. A subagent failure is not an exemption. A provider stream disconnect during the fan-out is not an exemption. Submit a proposal batch when you can complete the proposal work.
Report an execution problem or operator question through the terminal actions in the role skill.
```

### Passage 60: original lines 164–164

Category: `moved behind a pointer`. Destination: `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**. Trigger: submission of a proposal result. Read the skill before each command; an empty array records no valid work.

```text
An empty `proposals` array is legal. It is the honest submission when no proposal survived. A silent exit gives the system no information. The run then holds the pipeline of this Project until Culler reclaims it.
```

### Passage 61: original lines 166–167

Category: `unneeded`. The role cannot operate runner recovery or retry counters.

```text
The runner reports complete model responses and confirms connection recovery.
Connection recovery preserves the task outcome and its retry allowance.
```

### Passage 62: original lines 168–168

Category: `moved behind a pointer`. Destination: `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Execution problems and operator decisions**. Trigger: an execution problem or connection recovery.

```text
Do not submit connection recovery reports yourself.
```

## Original passages from `mc/verbs/skills/strategist/mc-strategist/SKILL.md`

### Passage 1: original lines 3–3

Category: `duplicate`. The trigger is shorter. `mc/verbs/directives.go`, **Your command surface**, retains the read-before-command rule and permitted-command boundary.

```text
description: The complete mc command surface for the Strategist. Load this skill before each mc call. The role can call only commands in this skill.
```

### Passage 2: original lines 8–10

Category: `unneeded`. Model, harness, and installation exposition adds no command choice. The generated pointer supplies the installed path.

```text
The agent profile sets the model and effort for this run.
The same command authority applies to Codex, the Claude Agent Software Development Kit (SDK), and OpenCode.
Culler supplies the harness configuration and the skills at `~/.agents/skills/`.
```

### Passage 3: original lines 12–12

Category: `duplicate`. The command forms retain run arguments; `mc/verbs/brief.go`, **Run**, identifies the value. `mc/verbs/directives/strategist.md`, **Culler contract** limits use to the assigned identity.

```text
Every write carries `--run <run_id>`. `<run_id>` is the `Run identifier` fact of the **Run** part of the run brief. Copy the value without change. `mc` rejects a call whose run identifier does not match the live run. `mc` applies no part of a rejected call. A terminal action is an `mc` command that ends the run.
```

### Passage 4: original lines 14–14

Category: `unneeded`. The expanded strict-JSON explanation becomes one object-only rule in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**. The command form retains `--batch -`.

```text
## Terminal action
```

### Passage 5: original lines 16–16

Category: `unneeded`. The expanded strict-JSON explanation becomes one object-only rule in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**. The command form retains `--batch -`.

```text
`--batch` accepts only `-`. `mc` reads the payload from stdin as strict JSON. An unknown field rejects the whole batch. Text after the closing brace also rejects the whole batch. This includes a second JSON value and any trailing text. Send the object alone.
```

### Passage 6: original lines 18–18

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Research**, outside the proposal payload.

```text
If you execute work inline instead of through subagents, state the reason in your response before the terminal action. Do not put the reason in a proposal. Do not append the reason to the payload. Culler keeps your response in the record of the run.
```

### Passage 7: original lines 22–22

Category: `unneeded`. The field prose becomes the schema table in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**. Required and optional fields retain their meaning.

```text
Payload shape: an object with one field, `proposals`. The `proposals` field is an array of proposal objects. Each proposal has these fields:
```

### Passage 8: original lines 24–28

Category: `unneeded`. The field prose becomes the schema table in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**. Required and optional fields retain their meaning.

```text
- The `title` field is required.
- The `project` field is optional.
- The `scope` field is optional. Its only value is `task`. An omitted `scope` means `task`.
- The `description` field is optional.
- The `priority` field is optional.
```

### Passage 9: original lines 30–30

Category: `unneeded`. The transaction prose becomes success and rejection rules in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**. No partial write becomes possible.

```text
`mc` validates every proposal inside one transaction. One violation of the constraints below rejects the entire batch. A rejected batch writes nothing.
```

### Passage 10: original lines 32–32

Category: `unneeded`. The expanded Project check becomes the `project` field constraint in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**.

```text
`mc` files every proposal into the pool of this run's Project. That Project id is the `Identifier` fact of the **Project** part of the run brief. An omitted `project` field defaults to that id. A present `project` field must equal that id. Any other id refuses the whole batch.
```

### Passage 11: original lines 34–34

Category: `unneeded`. The expanded title and description prose becomes their field constraints in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**.

```text
`title` is one line. It must be valid UTF-8 with no control characters, such as a newline or a tab. It must have a maximum of 4096 bytes. `description` has no such check. Put prose in `description`.
```

### Passage 12: original lines 36–36

Category: `unneeded`. The expanded priority prose becomes the `priority` field constraint in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**.

```text
`priority` is an integer from -1 to 3. The value -1 is expedited. The values 0 to 3 are P0 to P3. An omitted `priority` means 2. `mc` refuses any other value.
```

### Passage 13: original lines 38–39

Category: `duplicate`. The single batch-size rule remains at `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**.

```text
Propose two tasks in one batch. Submit fewer only when fewer valid tasks satisfy the Project vision.
`mc` rejects a batch with more than two tasks.
```

### Passage 14: original lines 41–41

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements**.

```text
## Acceptance criteria
```

### Passage 15: original lines 43–46

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements**.

```text
Each criterion describes task behavior and identifies evidence available in an agent container.
New or changed tests must pass on the submitted commit.
Whole-suite success requirements, such as `./mc/check.sh passes`, are invalid criteria.
An available suite command does not establish the capabilities of its tests.
```

### Passage 16: original lines 48–48

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements**.

```text
Criteria exclude these capabilities:
```

### Passage 17: original lines 50–54

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements**.

```text
- Access to the operator's host.
- A Docker daemon.
- macOS system interfaces.
- Additional container privileges.
- A hosted continuous integration service.
```

### Passage 18: original lines 56–56

Category: `unneeded`. The Strategist cannot perform the Verifier duties.

```text
Broad regression checks belong to the Verifier's duties for every task.
```

### Passage 19: original lines 57–57

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements**.

```text
An unchanged Project test failure alone permits a proposal that satisfies these rules.
```

### Passage 20: original lines 58–58

Category: `unneeded`. The Strategist does not choose Arbiter rejection categories.

```text
The Arbiter rejects invalid criteria through the `invalid_criteria` category.
```

### Passage 21: original lines 62–66

Category: `unneeded`. Exact history quotas and order do not change a proposal decision. History tests retain those delivery checks.

```text
The **Rejected work** and **Cancelled work** parts contain at most 20 historical tasks total from this Project.
Culler selects up to 10 cancelled tasks first.
Rejected tasks fill the unused places.
Each part lists the newest decisions first, with larger task numbers first for equal decision times.
An empty part is absent.
```

### Passage 22: original lines 67–67

Category: `unneeded`. The shorter historical-read sentence in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Reads** retains access to older tasks.

```text
Older tasks remain available through the permitted read commands.
```

### Passage 23: original lines 69–69

Category: `unneeded`. The row fields are visible. The scope lookup procedure covers absent descriptions. Exact excerpt lengths need no agent action.

```text
Each historical entry contains these fields:
```

### Passage 24: original lines 71–73

Category: `unneeded`. The row fields are visible. The scope lookup procedure covers absent descriptions. Exact excerpt lengths need no agent action.

```text
- The task number.
- The recorded title.
- The decision.
```

### Passage 25: original lines 75–76

Category: `unneeded`. The row fields are visible. The scope lookup procedure covers absent descriptions. Exact excerpt lengths need no agent action.

```text
A cancelled entry also contains a recorded reason excerpt when available, with at most 160 Unicode characters.
Historical entries exclude descriptions.
```

### Passage 26: original lines 77–77

Category: `unneeded`. One active-or-historical title rule in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Reads** replaces the narrower lookup formulation.

```text
Use `mc task get <id>` when a historical title leaves the scope unclear.
```

### Passage 27: original lines 78–78

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Proposal requirements**.

```text
Do not propose equivalent work from either historical part, even with a different title.
```

### Passage 28: original lines 79–79

Category: `duplicate`. Retained in `mc/verbs/brief.go`, **Cancelled work**.

```text
Use each cancellation reason as context for the operator's preference.
```

### Passage 29: original lines 80–80

Category: `unneeded`. The active rows already label their fields.

```text
The **Active tasks** part retains task numbers, titles, and stages.
```

### Passage 30: original lines 82–82

Category: `duplicate`. The list and lookup rules remain at `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Reads**. Subagent Project limits remain at `mc/verbs/directives/strategist.md`, **Research**; the path remains in `mc/verbs/brief.go`, **Project**.

```text
The **Active tasks** part of the run brief holds each task of this Project that `mc task list` can show. A sweep with `mc task list` thus adds no row of this Project. Use `mc task get <id>` when you need the description of one row. Your subagents read the committed files of this Project at the `Repository copy` fact of the **Project** part. Your subagents read no tree of any other Project.
```

### Passage 31: original lines 90–90

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Culler contract** and the permitted command surface. The command forms omit a task subject.

```text
## Out of scope
```

### Passage 32: original lines 92–92

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Culler contract** and the permitted command surface. The command forms omit a task subject.

```text
These operations belong to the operator and to Culler:
```

### Passage 33: original lines 94–96

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Culler contract** and the permitted command surface. The command forms omit a task subject.

```text
- create a task;
- start a run;
- decide a review packet.
```

### Passage 34: original lines 98–98

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Culler contract** and the permitted command surface. The command forms omit a task subject.

```text
Only the Arbiter promotes a proposal. Your run belongs to the Project, with no task subject.
```

### Passage 35: original lines 105–108

Category: `unneeded`. Failure counters and later scheduling do not change the choice between an execution problem and an operator decision.

```text
Dispatch, launch, Strategist proposal creation, and Arbiter proposal review share one Project infrastructure allowance of three counted failures.
The third counted failure stops each new agent run with the Project lease in the Project.
A successful proposal batch restores that Project allowance. An operator decision report preserves its count.
The operator resolves the shared Project decision before proposal work resumes.
```

### Passage 36: original lines 109–109

Category: `unneeded`. The concise direct-observation rule in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Execution problems and operator decisions** retains the diagnostic limit.

```text
An agent diagnosis supplies an unknown cause. Culler requires direct runner or resident evidence for shared connection conditions.
```

### Passage 37: original lines 111–114

Category: `unneeded`. The concise recovery rule in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Execution problems and operator decisions** preserves continuation and leaves connection reports to the runner without listing its commands.

```text
The runner owns `mc run model-response` and `mc collie model-response`.
These commands record complete model responses for the current attempt.
Do not call these runner commands.
Continue the assigned work after connection recovery.
```

### Passage 38: original lines 118–120

Category: `duplicate`. Retained at `mc/verbs/directives/strategist.md`, **Project role customization**.

```text
The Culler contract has priority over custom instructions.
Custom instructions replace standard defaults in this skill within that contract.
Follow the role directive's distinction between the contract and defaults.
```

### Passage 39: original lines 121–121

Category: `unneeded`. The shorter conflict trigger remains at `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Project role customization**.

```text
If a custom instruction conflicts with the contract during work, submit this terminal:
```

### Passage 40: original lines 125–125

Category: `unneeded`. The concise field list in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Project role customization** retains every request field. JSON is defined once in **Proposal batch**.

```text
The JavaScript Object Notation (JSON) object contains these fields:
```

### Passage 41: original lines 127–128

Category: `unneeded`. The concise field list in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Project role customization** retains every request field. JSON is defined once in **Proposal batch**.

```text
- The `run` field: your run identifier.
- The `revision` field: the customization revision from the run brief.
```

### Passage 42: original lines 130–130

Category: `unneeded`. The concise field list in `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Project role customization** retains every request field. JSON is defined once in **Proposal batch**.

```text
- The `requirement` field: the Culler contract requirement that the text contradicts.
```

### Passage 43: original lines 133–137

Category: `unneeded`. The role cannot control subject stages, role blocks, Project pauses, or failure counters after this terminal.

```text
The command records the Project, role, and subject from your run.
It ends the run with a customization conflict and preserves the subject's stage and accepted work.
A run without a subject blocks that role within its Project.
The Project retains its pause state.
The command preserves all failure and correction allowances.
```

## Generated explanations and pointers

These entries quote the delivered base text from `mc/verbs/brief.go`.
The before and after captures use the same data fixture.

### `mc/verbs/brief.go`: Project role customization

Category: `duplicate`. Retained authority: `mc/verbs/directives/strategist.md`, **Project role customization**. The new explanation identifies the data only.

Original text:

````text
These operator instructions replace standard defaults within the Culler contract. A contract conflict ends the run with a customization conflict.
````

Final explanation:

> This part identifies the operator's custom instructions and their revision.

### `mc/verbs/brief.go`: Run

Category: `unneeded`. The new explanation identifies the command placeholder directly.

Original text:

````text
This value identifies this run. It is the `<run_id>` value of each `mc` command.
````

Final explanation:

> Use this identifier as `<run_id>` in command forms that require it.

### `mc/verbs/brief.go`: Agent credentials

Category: `unneeded`. The role cannot change model selection, host credentials, or proxy operations.

Original text:

````text
The agent profile selects the harness, model, and effort; the same role input applies to Codex, Claude, and OpenCode. Culler keeps provider credentials on the host and supplies this container with temporary proxy access.
````

This part is absent from the final Strategist brief.

### `mc/verbs/brief.go`: Project environment

Category: `unneeded`. The new explanation identifies the supplied environment. `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Research dependencies** controls optional dependency use.

Original text:

````text
Culler supplies the selected image and persistent Project home. A task run receives a fresh source workspace and dependency preparation before the agent starts.
````

Final explanation:

> These identifiers name the selected Project environment.

### `mc/verbs/brief.go`: Active tasks

Category: `duplicate`. The exclusion rule remains at `mc/verbs/directives/strategist.md`, **Proposal requirements**; the lookup rule remains at `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Reads**.

Original text:

````text
Each proposal and each active task of this Project, by title. Do not propose work that one of these rows covers; `mc task get <id>` returns the description of a row.
````

Final explanation:

> These proposals and active tasks are absent from the repository copy.

### `mc/verbs/brief.go`: Rejected work

Category: `unneeded`. The exact history quota and field tour add no decision rule. `mc/verbs/directives/strategist.md`, **Proposal requirements** retains the duplicate check.

Original text:

````text
These recent rejected tasks fill unused places after cancellations, up to 20 historical tasks total, with the newest decisions first. Each entry contains the task number, title, and decision; use it to assess repeated work.
````

Final explanation:

> These are recent rejected tasks.

### `mc/verbs/brief.go`: Cancelled work

Category: `unneeded`. The exact quota and excerpt length add no decision rule. The replacement retains cancellation reasons as operator context.

Original text:

````text
This limited history contains up to 10 recent cancelled tasks within the total limit of 20, newest decisions first. Each entry contains the task number, title, decision, and available reason excerpt of at most 160 Unicode characters.
````

Final explanation:

> These are recent cancelled tasks, with reason excerpts when available. Use each reason as context for the operator's preference.

### `mc/verbs/brief.go`: Failure controls

Category: `unneeded`. The role cannot operate other roles, recovery controls, or failure counters. `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Execution problems and operator decisions** retains its actionable terminal choices.

Original text:

````text
Execution problems and operator decisions use separate controls. A confirmed connection condition uses the connection controls.

```json
[
  "The runner confirms connection recovery from complete model responses. Recovery preserves the task outcome and its retry allowance.",
  "Dispatch and launch failures use the Project infrastructure allowance before the agent process starts.",
  "Strategist proposal creation and Arbiter proposal review share that Project allowance throughout their runs.",
  "Assigned task failures use their task run failure allowance after the agent process starts.",
  "Each failure allowance permits three counted failures. An operator decision report consumes zero failure units."
]
```
````

This part is absent from the final Strategist brief.

### `mc/verbs/brief.go`: Vision

Category: `duplicate`. The Project goal remains at `mc/verbs/directives/strategist.md`, **Culler contract** and the batch size at `mc/verbs/skills/strategist/mc-strategist/SKILL.md`, **Proposal batch**.

Original text:

````text
This is the current content of `VISION.md`, the goal of the Project. Propose two tasks that advance this goal.
````

Final explanation:

> This is the accepted `VISION.md` content.

The **Project** and **Proposal decisions** explanations remain unchanged.
The Project explanation supplies the read-only committed repository path.
The Proposal decisions explanation requires every relevant operator resolution.
The shorter customization explanation leaves the operator's exact text and revision intact.

### `mc/verbs/directives.go`: Your command surface

Category: `unneeded`. The verbose help and search warning adds no authority beyond the concise permitted-command rule.
The pointer retains the installed path and requires the skill before each command.

Original text:

```text
Every `mc` command available to you, with its exact flags and constraints, is the mc-strategist skill, installed in this container at `~/.agents/skills/mc-strategist/SKILL.md`. Load it before your first `mc` call and again before you submit your terminal.

`mc` ships no help surface: bare `mc` names zero verbs and `mc --help` is an unknown verb. That skill is the complete surface your role may call. A verb absent from it does not exist for you, and a search for one wastes the run.
```

Final pointer:

```text
Read `~/.agents/skills/mc-strategist/SKILL.md` before each `mc` call.
The skill lists the permitted commands; `mc` has no help command.
```

The **Your other skills** pointer block remains byte-for-byte unchanged.
The command skill frontmatter changes its description to cover commands and the conditional procedures it holds.
That description is included in the skill inventory above.
No new skill pointer requires another file or a host path.

## Test coverage

The changed text tests keep the inline reason, Project scope, dependency checks, and customization conflict at their retained locations.
They stop requiring copies in both the directive and skill.
The Arbiter's existing assertions remain intact.
A new delivery test checks the Strategist's evidence, conditional pointers, authority, and one-copy rules.
A payload test checks that every retained brief part preserves its facts, rows, and exact text.
Existing tests retain history limits, ordering, Project isolation, Vision delivery, and command conformance.

The execution report in the task artifacts records command results and every unavailable check.
