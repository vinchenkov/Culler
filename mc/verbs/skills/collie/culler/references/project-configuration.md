# Project configuration

Inspect the current Project and its readiness before a change.
Use command help for registration previews, registration saves, and initialization controls.
A registration preview identifies the exact workspace effect.
Host operations perform authorized workspace writes while Collie's Project mount stays read-only.
Pipeline agents perform ordinary Project implementation work.

## Vision

A Vision draft lives in Culler storage outside the workspace.
Each draft edit uses the revision from a current read.
A conflict requires a fresh read and reconciliation with the operator's edits.
Repository evidence describes existing work; it does not establish the operator's goals.
An ordinary conversation can target an explicit Project.
A dedicated Vision conversation remains bound to its associated Project.

Discuss ideas and attachments without an edit when no edit is necessary.
Describe the current draft revision and workspace effect before a Vision save.
An authorized Vision save writes the workspace file through the host operation.
A draft edit alone leaves that file unchanged.
Report an accepted save separately from its completion.

## Environment

Read the current setup phase, selected environment, proposals, and reports.
The Setup Journal shows initial setup; the Environment page retains later configuration and history.
Workspace dependencies use ordinary container permissions.
Image dependencies require approval of the exact proposed configuration and Dockerfile.
A build runs only through the host owner and checks Culler runtime compatibility.
Image approval requires a continuation report before initial setup completes.
An unavailable build or test remains a reported limit under the existing source acceptance rules.

Use the applicable save, approve, deny, assist, retry, or cache-clear operation.
Keep the environment operation identifier after acceptance.
Read its state before a retry after a timeout.
The command state checks decide whether initialization or development can start.

## Project controls

A Project pause permits active work to finish and leaves Collie available.
An ordinary Collie conversation can use authorized global pause and resume operations.
A dedicated Vision conversation uses `mc project pause` and `mc project resume` for its own Project.
Global resume selects only Projects with complete initialization.
A resume preserves independent role blocks, task conditions, and required receipt recovery.
An archive or deletion uses the operation's current domain requirements.
Read and present the current deletion preview before confirmation.
Use the exact Project identifier and required confirmation in the deletion request.

## Role instructions

Read the role contract, current instructions, revision history, and conflict reports.
The Culler contract retains authority over custom instructions.
Save the requested instructions with the current expected revision.
An empty instruction string removes the customization for new runs.
A stale revision requires a fresh read and reconciliation that preserves the proposed text.
A save affects new runs and preserves each block and the Project pause state.
Resolve a role block separately with its conflict record and reason.
Report the Project, role, saved revision, and saved instructions.
