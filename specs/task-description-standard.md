# Task description standard

## Purpose

The Strategist and Collie both write task descriptions.
A description that Collie writes for the operator must have the same parts as the description of a Strategist proposal.
One shared skill holds the standard for a task description.
The Strategist, the Arbiter, and Collie apply that skill.
The standard has one source, and no other agent input restates it.

## Current gap

These facts describe the system before this change:

- The Strategist role directive holds the description rules and the acceptance criteria rules.
- The Arbiter role directive restates the acceptance criteria rules and the list of absent container capabilities.
- The `mc-collie` skill tells Collie only to put the task requirements in the description.
- A task that Collie creates starts at `seeded`. The Arbiter never judges it.
- The Collie command surface has two commands that create a task: `mc task add` and `mc board write` with the `create` action.

## Scope of the standard

The standard applies to the task description only.
The description holds every requirement of the task.
The Worker and the Verifier receive the description and no other proposal text.

A Strategist proposal also holds a Strategic rationale.
The Strategic rationale is evidence for the Arbiter and the operator, and it adds no requirement to the task.
The rules for the Strategic rationale stay in the Strategist role directive.

## The shared skill

The skill `task-description` exists at `mc/verbs/skills/shared/task-description/SKILL.md`.
The skill directory name and the frontmatter `name` are both `task-description`.
The frontmatter `description` tells an agent to load the skill before it writes, revises, or judges a task description.

The skill holds only the standard for the content of a task description.
The skill names no `mc` command.
The skill holds no rule for the selection or the rank of work.
The skill uses the term "acceptance criterion" for each checkable requirement of a task.
The skill follows the rules of the shared `writing` skill.

### Parts of a task description

The skill states these rules for the parts of a task description:

- The task delivers one Git commit to the Project repository. One task can change several components.
- The description states the scope: what the task changes and what stays unchanged.
- The description states the acceptance criteria.
- The description holds every requirement of the task. The Worker and the Verifier can act on the description alone.
- The writer inspects the relevant code and tests. The scope and each acceptance criterion rest on that evidence.

### Rules for acceptance criteria

The skill states these rules:

- Each acceptance criterion describes behavior within the scope of the task.
- Each acceptance criterion names a check that an agent container can run.
- Each check runs without these capabilities:
  - access to the operator's host;
  - a Docker daemon;
  - macOS system interfaces;
  - additional container privileges;
  - a run on a hosted continuous integration (CI) service.
- The writer confirms from the tests themselves that each required check can run in an agent container.
- Each acceptance criterion is specific to the task and excludes whole-suite success.
- Broad regression checks belong to the Verifier.
- New or changed tests must pass on the submitted commit.
- An unchanged Project test failure alone keeps a task valid.

## Culler contract

The rules of the `task-description` skill belong to the Culler contract of the Strategist and of the Arbiter.
The Culler contract section of each of these two role directives states this.
A Project role customization that contradicts a rule of the skill is a contract conflict.

## Strategist

The Strategist role directive tells the Strategist to apply the `task-description` skill to each proposal description.
The directive keeps these Strategist rules:

- The proposal batch contains two task proposals.
- The rules in "Finding high-leverage work" select the work.
- The research and the fact checks that support the selection and the Strategic rationale.
- The exclusion of equivalent work in the repository, in active tasks, in rejected tasks, and in cancelled tasks.
- The rules in "Write the Strategic rationale".
- The Arbiter selects among the proposals. The Strategist does not rank them.

The directive holds no rule that the `task-description` skill states.
The `mc-strategist` skill keeps the proposal command, its field table, and the terminal commands of the Strategist.

## Arbiter

The Arbiter role directive tells the Arbiter to judge each proposal description against the `task-description` skill.
The Arbiter rejects a proposal that violates a rule of the skill.
The Arbiter uses the `invalid_criteria` category for a violation of an acceptance criteria rule.
The rejection reason names the invalid criterion and the rule of the skill that it violates.

The Arbiter role directive and the `mc-arbiter` skill keep these Arbiter rules:

- the rank of proposals and the use of the Strategic rationale;
- the rules for overlap and duplicates;
- the all-reject batch and the comparison with historical work.

Neither the directive nor the `mc-arbiter` skill restates a rule that the `task-description` skill states.

## Collie

The Collie directive at `mc/verbs/directives/collie.md` tells Collie to apply the `task-description` skill to each task description that Collie writes.
This rule applies to a new task and to a requirements revision.
The operator request selects the work. Collie writes the description from that request and from repository evidence.

Before Collie creates a task, Collie shows these items to the operator:

- the title, the Project, and the priority;
- the full description.

Before Collie requests a requirements revision, Collie shows the full revised description and the reason.
The operator can change or remove any part of the draft.
Collie identifies each rule of the standard that the changed draft violates before the operator confirms.
Collie submits the text that the operator confirms.

### One command to create a task

Collie creates each task through `mc board write` with the `create` action.
The `mc-collie` skill lists no other command that creates a task.
The `mc-collie` skill states the priority rules for a task that the `create` action creates.
The Collie command surface excludes the `task.add` verb.
`mc collie start` refuses an allowlist that names `task.add`.
`mc task add` remains available to host invocations.

## Constraints

- The task storage, the task stages, and the task origins stay the same.
- A Strategist proposal starts at `proposed`. A Collie task starts at `seeded`.
- Only a Strategist proposal holds a Strategic rationale.
- The Verifier skill keeps its rules for unavailable checks.
- Each agent input that this change affects describes only the new model and holds no history of the change.

## Acceptance criteria

- The skill sets of the Strategist, the Arbiter, and Collie include the `task-description` skill.
- A test proves that each rule of the skill occurs once in the complete agent input of the Strategist.
- A test proves that each rule of the skill occurs once in the complete agent input of the Arbiter.
- A search of `mc/verbs/directives/` and `mc/verbs/skills/` finds the acceptance criteria rules only in the `task-description` skill and the Verifier rules for unavailable checks.
- A test proves that `mc collie start` refuses an allowlist that names `task.add`.
- A test proves that a host invocation of `mc task add` creates a task at `seeded`.
- The fast suite in `AGENTS.md` passes.
