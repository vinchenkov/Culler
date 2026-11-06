---
name: mc-packager
description: The complete and only mc verb surface for the Packager role; load before any mc call and before submitting the terminal — mc ships no --help, and a verb absent from this skill does not exist for this role.
---

# mc — Packager

The agent profile sets the model and effort for this run.
The same command authority applies to Codex, the Claude Agent Software Development Kit (SDK), and OpenCode.
Culler supplies the harness configuration and the skills at `~/.agents/skills/`.

`<run_id>` is the `Run identifier` fact of the **Run** part of the run brief. Each `mc complete` call carries it as `--run <run_id>`. `mc run infra-failure` takes it as its first argument. `mc task block` and the reads take no run identifier. `<task>` is the `Identifier` fact of the **Task** part. `<id>` in a read is the identifier of any task. `mc` rejects a call whose run identifier does not match the live run. It applies no part of that call. A terminal is an `mc` command that ends the run.

## Terminal

Submit exactly one terminal. This terminal requires both `--outputs` and `--thesis`.

- `mc complete <task> --run <run_id> --status packaged --outputs <path> --thesis <prose>`

`--thesis` carries the summary of the packet as prose. It is not a path. The operator reads the summary on the review queue card before the operator opens any file. `mc` refuses a summary that is blank, that is longer than the limit in your directive, or that holds a control character.

Render the packet inside one plane, in the folder of this task. A plane is one durable directory that Culler mounts under `/mc/artifacts/`. Its content stays after the run ends. The path is `/mc/artifacts/<plane>/<task>/packet/packet.html`. Pass that absolute path to `--outputs`. The file name is `packet.html`. The Packet format section of your directive governs the shape of the file.

The dashboard supplies the off-white page background and the base typography.
Keep page containers transparent.
Preserve the page style when you copy a previous packet.

The dashboard selects the display from the file extension. List `/mc/artifacts/` and use a plane directory that already exists. The plane is writable. Create the folder with `mkdir -p`.

Do not write directly at `/mc/artifacts/<your-file>`. Do not create a new directory at that level and write into it. `/mc/artifacts/` itself is ordinary container storage. A write there succeeds and looks correct. The end of your run destroys it.

The terminal opens the file before it records anything. It refuses a path with one of these properties:

- the path is not inside a mounted plane;
- the file is absent;
- the file is empty;
- the file is larger than 64 MiB;
- the file is not a regular file.

A refusal writes nothing. Do not answer a refusal with a path that you did not write. Fix the render. If `/mc/artifacts/` contains no plane directory, the run has no durable location for a packet. In that case, report `--infra`. Do not record a render that nobody can read.

## Repository

Apply the project-wiki skill to original deliverables and task execution reports.
Requested reports and original project documents reside in the submitted commit outside `openwiki/`.

The repository at `/workspace/source` is an independent copy of the Project repository. It starts at the `Start commit` fact of the **Git state** part. It is also your working directory. The Project repository itself stays outside your write access.

Preserve the start commit and its files. Make no commit in `/workspace/source`. The terminal refuses the packet when `HEAD` differs from the start commit. The terminal also refuses the packet when `git -C /workspace/source status --porcelain=v1 --untracked-files=all` prints a path. A file that the Project ignores does not count.

Use `/tmp` for each intermediate file. A build that writes only ignored files keeps the repository clean. For each other build, copy the repository to `/tmp` and build in the copy. `/tmp` ends with the container, so do not record a path that points into it.

### Read the reviewed change

Read the reviewed change with Git in `/workspace/source`. Do not recall it from a report. The reviewed change is the change that the work adds to the destination branch. The reference `refs/mc/destination` in your repository names the commit of the destination branch.

The reviewed range starts at the newest commit that the destination branch and the `Submitted commit` fact of the **Git state** part share. This command prints that commit:

    git -C /workspace/source merge-base refs/mc/destination <Submitted commit>

That commit is the `<range start>`. The reviewed range ends at the `Submitted commit` fact. This command shows the diff of the reviewed change:

    git -C /workspace/source diff <range start> <Submitted commit>

The work can hold a merge of the destination branch. The diff from the `<range start>` holds only the change of the work.

If a Git command fails, report `--infra`.

The Files changed view of the dashboard shows the operator the file list, the full diff, and the commit identifiers. The packet leaves them out.

### Preserve verification evidence

Read the evidence file from the **Latest verdict** part.
Include its material results in the Review Packet:

- The evidence for each criterion.
- Broad check results and any base comparisons.
- Unchanged failures and their causes.
- Skipped or unavailable checks and their reasons.
- Each `UNVERIFIED` entry and the coverage that remains unverified.
- Incomplete comparisons and their effect on the verdict.

Keep actual command results separate from the task verdict.
A command that exits nonzero remains unsuccessful even when the task passes.
A command that exits zero succeeds; its skipped tests remain unverified.
Unchanged failures and unavailable host tests remain information for the operator.
These observations alone create no mandatory manual check or additional approval requirement.

### Check a term

The Terms section of your directive separates a core term, an established term, and a new term. This command shows each line of the Project at the `<range start>` that holds a term:

    git -C /workspace/source grep -i -- <term> <range start>

A result in a document, in the wiki, or in user interface text shows that the Project used the term. No result shows that the term is new.

## A new packet for a subject that has a packet

This case applies when the run brief has a **Previous packet** part. Culler published that packet for an earlier submitted commit of this subject.

The new packet is a copy of the previous packet with its corrections. It states the difference from the previous packet first, so the operator can read only the difference.

### Find the difference

The `Submitted commit` fact of the **Previous packet** part is the `<previous commit>`. Run these three commands in this order:

    git -C /workspace/source merge-base refs/mc/destination <previous commit>
    git -C /workspace/source merge-tree --write-tree --merge-base=<previous start> <previous commit> <range start>
    git -C /workspace/source diff <tree> <Submitted commit>

- The first command prints the start of the previous reviewed range. That commit is the `<previous start>`.
- The second command applies the previous reviewed change to the `<range start>`. Its first output line is the `<tree>`. The command ends with status 1 when the two changes conflict. That status is a correct result.
- The third command shows the difference.

The difference holds each change of the work that the previous packet does not describe. A removed line with a conflict marker shows a merge conflict that the work resolved. An empty difference shows that the reviewed change is the same.

### Write the new packet

Copy the file of the `File` fact of the **Previous packet** part to the path of the new packet. Give the copy write permission:

    cp <File> /mc/artifacts/<plane>/<task>/packet/packet.html
    chmod u+w /mc/artifacts/<plane>/<task>/packet/packet.html

Then edit the copy:

- Keep each statement that the current records support.
- Correct each statement that the difference makes false.
- Write the **Criterion evidence** section from the current `Evidence file`.
- Keep an evidence item only when the difference leaves its subject unchanged.
- Add the **Changes since the previous packet** section directly after the **Summary** section. If the copy holds a section with that name, replace that section.

The **Changes since the previous packet** section states these items in prose:

- the cause of the new packet, as the last completion report states it;
- each change of the work in the difference;
- each other section that you changed, and the reason.

When the difference is empty, the section states that the reviewed change is the same.

The new summary replaces the summary of the previous packet. Write the current claim.

## A run that cannot give a result

Two events end a run without a result. The event selects the command. Each command in this section ends your run, so submit exactly one. No command here replaces a finished packet.

### A decision that only the operator can make

These are examples of such decisions:

- Two criteria contradict each other.
- A criterion has no measure that you can check.

The reason states the decision that you need and why. The command sets Decision Pending on the task. The operator resolves the decision with a requirements revision, or the operator cancels the task.

- `mc complete <task> --run <run_id> --needs-operator --reason <prose>`
- `mc task block <task> --reason <prose>`

The two commands record the same decision. Use the first command. The second command applies to your own task only and takes no `--run` flag.

### A task run failure

Report the condition that prevents completion:

- `mc run infra-failure <run_id> --condition <observed condition>`
- `mc complete <task> --run <run_id> --infra --reason <observed condition>`

The assigned task owns three task run failure attempts.
Each failed run after process start consumes one attempt.
The first failure permits an automatic retry after 30 seconds.
The second failure permits an automatic retry after two minutes.
The third failure sets `stalled`, which the dashboard shows as Retries Exhausted.
Forward stage progress or an operator task resume restores three attempts.
A task resume preserves an independent Decision Pending condition.

Dispatch and launch failures before process start consume the Project infrastructure allowance.
A required operator decision consumes zero failure or correction attempts.
Only direct execution observations can establish a connection condition.
An agent report retains an unknown cause.

The runner owns `mc run model-response` and `mc homie model-response`.
These commands record complete model responses for the current attempt.
Do not call these runner commands.
Continue the assigned work after connection recovery.

## Project role customization

The Culler contract has priority over custom instructions.
Custom instructions replace standard defaults in this skill within that contract.
Follow the role directive's distinction between the contract and defaults.
If a custom instruction conflicts with the contract during work, submit this terminal:

- `mc run customization-conflict --request <json>`

The JavaScript Object Notation (JSON) object contains these fields:

- The `run` field: your run identifier.
- The `revision` field: the customization revision from the run brief.
- The `passage` field: the exact conflicting text from that revision.
- The `requirement` field: the Culler contract requirement that the text contradicts.
- The `reason` field: why both instructions cannot hold.

The command records the Project, role, and subject from your run.
It ends the run with a customization conflict and preserves the subject's stage and accepted work.
A run without a subject blocks that role within its Project.
The Project retains its pause state.
The command preserves all failure and correction allowances.
