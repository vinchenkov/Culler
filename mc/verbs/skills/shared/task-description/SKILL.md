---
name: task-description
description: The standard for a task description, its parts and the rules for its acceptance criteria. Load it before you write, revise, or judge a task description.
---

# Task description

Apply this skill to each task description that you write, revise, or judge.
An acceptance criterion is a checkable requirement of a task.
Write the description under the `writing` skill.

## Parts of a task description

- The task delivers one Git commit to the Project repository. One task can change several components.
- The description states the scope: what the task changes and what stays unchanged.
- The description states the acceptance criteria of the task.
- The description defines the complete required result. Revised runs also use revision instructions to identify corrections.
- Base the scope and each acceptance criterion on evidence from the relevant code and tests.

## Rules for acceptance criteria

- Each acceptance criterion describes behavior within the scope of the task.
- Each acceptance criterion names a check that an agent container can run.
- Each check runs without these capabilities:
  - access to the operator's host;
  - a Docker daemon;
  - macOS system interfaces;
  - additional container privileges;
  - a run on a hosted continuous integration (CI) service.
- Confirm from the tests themselves that each required check can run in an agent container. An available command does not prove that its tests can run.
- Each acceptance criterion is specific to the task. Do not require whole-suite success, such as "all Project tests pass".
- Broad regression checks belong to the Verifier.
- New or changed tests must pass on the submitted commit.
- An unchanged Project test failure alone keeps a task valid.
