# Durable storage checks

The checks made no model calls.
The checks used isolated persistent directories and disposable source worktrees.

## Verified behavior

- The framework rejects temporary record paths and symbolic links into temporary storage.
- The framework stops dependent work after primary directory loss or backup failure.
- A new experiment preserves an existing backup for the same record path.
- Record recovery preserves the journal, candidate directives, packet artifacts, and native checkpoint.
- Native continuation reuses completed results after record recovery.
- Source recovery restores the exact source files after task worktree deletion.
- The source archive excludes an untracked credential fixture.
- The browser restores recovered history without launch receipts.
- The browser resumes that experiment only after the explicit action.
- The browser shows the record directory and backup directory.
- The terminal review accepts actual output at 80 and 40 columns, with color and without color.

## Test results

The complete model-free suite returned 635 passes and one failure.
A source edit during that run invalidated the frozen source for the browser recovery test.
That test passed after the source edits stopped.

The focused command and browser server checks returned 81 passes.
The evaluator, repository disposal, and continuation checks returned 117 passes.
The storage and record checks returned 18 passes after the final backup protection.
The browser display and storage checks returned 10 passes.
The checks include overlapping tests.

`git diff --check` passed.
