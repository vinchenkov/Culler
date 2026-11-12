---
name: mc-packager
description: Read before repository work or an mc call for Packager command forms and repository procedures.
---

# mc — Packager

`mc --help` describes operator commands; it grants no additional role authority.
Use this role skill for assigned run commands and terminal submissions.

The **Run** and **Task** parts of the run brief define the command arguments.
A call with another run identity fails without a state change.

## Repository

Culler supplies an independent Project repository copy at `/workspace/source`, your working directory.
The original Project repository stays outside your write access.
Preserve its `Start commit` from **Git state** and keep its files unchanged.
Make no commit or branch change.
Before submission, `HEAD` must equal that commit and this command must print no path:

    git -C /workspace/source status --porcelain=v1 --untracked-files=all

Ignored files do not count.
Use `/tmp` for intermediate files.
A build can use the repository when it writes only ignored files.
For other builds, copy the repository to `/tmp` and build there.
Temporary files disappear with the container.

### Read the reviewed change

Culler supplies `refs/mc/destination`, the destination branch commit, in the repository copy.
Use the `Submitted commit` from **Git state** as `<Submitted commit>`.
The reviewed range starts at the newest commit that those commits share.
Find that commit with Git:

    git -C /workspace/source merge-base refs/mc/destination <Submitted commit>

Use that result as `<range start>` to read the reviewed diff:

    git -C /workspace/source diff <range start> <Submitted commit>

This range excludes unrelated destination changes, including when the work merges the destination branch.
For an unsuccessful repository command, use **A run without a packet** below.
The packager-previous-packet skill defines its expected comparison conflict status.

### Novelty claims

Before a novelty claim, inspect the reviewed repository and report what you find, or omit the claim.
A novelty claim asserts one of these properties:

- The capability is the first of its kind.
- The capability is the only one of its kind.
- The capability was absent before the change.

Report observations without a new judgment that the work duplicates another change or requires rejection.

### Check a term

The directive's **Terms** section defines the term classes for the packet.
Use the `<range start>` from **Read the reviewed change** to check prior use:

    git -C /workspace/source grep -i -- <term> <range start>

Matches in code, documents, the wiki, or interface text establish prior Project use.
No match means that the term is new.
Git returns status 1 for that result.

## Packet result

List `/mc/artifacts/` and select an existing plane directory.
A plane is a durable directory that Culler mounts for artifacts.
Create the task's packet directory there with `mkdir -p`.

Write `/mc/artifacts/<plane>/<task>/packet/packet.html` and pass that absolute path as `<path>` below.
Use only an existing plane.
Other directories directly under `/mc/artifacts/` are temporary container storage.
The packet must be a nonempty regular file of at most 64 mebibytes.
Apply the directive's **Packet format** requirements to its contents.

- `mc complete <task> --run <run_id> --status packaged --outputs <path> --thesis <prose>`

Both flags are required.
The `--thesis` value is your overview text, not a path or the packet title.
Write one nonblank paragraph of at most 4096 bytes without control characters.
State the delivered change and why it matters.
With **Strategic rationale**, apply the directive's **Strategic rationale** requirements to that text.
Culler also presents this text in the review queue and packet conversations.

A refused submission records nothing.
Correct the reported condition and submit again, or use **A run without a packet**.
Use that failure procedure when no durable plane exists.

## A run without a packet

Use this procedure when a required input or tool fails, or completion requires an operator decision.
Continue the assigned work after a connection recovers.

### Required operator decision

Report a decision that only the operator can make, such as contradictory criteria or a criterion without a measure.
State the required decision and its cause:

- `mc complete <task> --run <run_id> --needs-operator --reason <prose>`

This command sets Decision Pending on the task.
It uses no failure or correction attempt.
The equivalent form applies only to your assigned task and takes no run argument:

- `mc task block <task> --reason <prose>`

Use the first form.

### Task run failure

Report the observed condition that prevents completion with one of these equivalent forms:

- `mc run infra-failure <run_id> --condition <observed condition>`
- `mc complete <task> --run <run_id> --infra --reason <observed condition>`

Retain an unknown cause unless direct execution observations establish it.

## Project role customization

Use this procedure when **Project role customization** conflicts with the directive's **Culler contract**.
Submit the conflict through this terminal action:

- `mc run customization-conflict --request <json>`

The JavaScript Object Notation (JSON) object holds these fields:

- The `run` field: the `<run_id>` of this run.
- The `passage` field: the exact conflicting text from the custom instructions.
- The `requirement` field: the contract requirement that the text contradicts.
- The `reason` field: why both instructions cannot hold.
