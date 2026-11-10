# Dashboard pages

Culler provides six main pages and dedicated Project Vision and Environment pages. The dashboard serves one operator.
The dashboard command interface reaches the database only through `mc`.
Operator text names no `mc` command.

The page column of each page has a maximum width of 1920 pixels (px).
The side space of the page column is 24 px at a window width of 800 px and more.
Text areas keep a line length that is easy to read.
In the System health card of Configs, the actions of each condition stay near the text of that condition.

## Page display

Each page shows its structure before its first reads complete.
Data fills the visible structure as each read completes.
The page keeps its controls and content areas in place during each read.
A failed read preserves the available content and shows the applicable repair action.
A successful page read clears a page error that appeared before the read started.
A repeat visit shows retained content when that content remains available.

Page changes preserve the positions of the header controls.
Page content appears without an entrance animation.
The page width reserves space for the browser scrollbar.
A window focus change preserves the visible page layout.
The system status preserves its last result until a new read completes.
A refresh preserves the page scroll position when it restores focus to a control.
A hidden document preserves the Board command bar and its draft.
A repeat textarea measurement preserves the page scroll position when the text and width remain equal.

## Project context

The page title names the selected Project on these pages:

- Board
- Review
- Pipeline

Each page displays records for exactly one selected Project.
The Project name is a button that opens the Project list and its controls.
The button shows the selected Project name with one or several Projects.

The list uses the Project registry, including Projects without tasks and archived Projects.
Each list entry shows the Project title, its pause state, and its lease state.
Each list entry contains the pause control of its Project.
The list footer contains bulk pause controls when at least two Projects have complete setup and are not archived.
Each list entry identifies its Project in its tooltip.

The selected entry has a distinct appearance and an accessible selected state.
The list supports the keyboard: the arrow keys move, Enter selects, and Escape closes.
A click outside the list closes it and keeps the selection.
A long Project name truncates with an ellipsis.
The page tabs wrap below the title when space is limited.

Each list entry shows its full Project title in a tooltip.
On a narrow screen, the title stacks and the selector takes the full width.

The browser preserves the selected Project across page changes and reloads.
An absent selection defaults to the first active or paused Project.
An archived Project can remain selected.
A missing saved Project uses the same default rule.

An empty registry hides the Project context in the header.
The Board shows the empty state and the **Add Project** action.
A failed registry read clears project records and disables Project actions until the next successful read.

A Project change clears the previous packet selection and decision form.
It closes the task dialog and clears the Activity streams.
A response for an earlier Project cannot update the selected Project's page.
A slow packet response cannot restore the previous packet or its decision controls.

Collie, Configs, and System Logs are global pages. These pages keep the Project button visible, muted, and disabled.
The Project button keeps its position and its selected Project name across page changes.
The navigation places Board, Review, and Pipeline before a vertical divider.
Collie, Configs, and Logs follow the divider. The groups have no visible labels.
The first paint includes the Project button and the correct page highlight.
The Project button keeps its space before data arrives.
The Board, Review, and Pipeline tabs have equal spaces between their labels.
The Review count sits on the corner of the Review tab and moves no page tab.
A saved Project name remains visible during a refresh. The button waits for current Project data before it becomes available.
The browser preserves the project selection while the operator uses a global page.
The header lease badge follows the Project selection on Project pages.
On a Project page, the badge names the role, the task, and the run time of the Project run.
A run in a paused Project shows "finishing run" in place of "running".
On a global page, the badge shows the number of active runs and the number of finishing runs.
The badge tooltip on a global page names the Project title, the role, and the task of each run.
A stale heartbeat or a pending stop appears first in the badge text.
The badge counts only agent runs that hold the Project lease. A repository operation and a wiki update run hold no Project lease, so the badge omits them.
The badge text stays on one line. The header height depends only on the window width.
The Review badge counts the selected Project's live packets.

## Viewer links

AgentsView is the trace viewer.
It shows the traces of pipeline runs, Collie sessions, and setup agent operations.
The OpenWiki viewer shows the wiki of one Project.
The header shows two links after the page tabs:

- **Wiki** opens the OpenWiki viewer of the selected Project.
- **Traces** opens the list of all traces in AgentsView, including one-shot and automated sessions.

A viewer link opens a new browser tab.
A viewer link is separate from the page tabs. Its appearance differs from the page tabs.
The accessible name of each viewer link states that the link opens a new tab.
The new tab receives no referrer and no reference to the dashboard window.
AgentsView receives no dashboard credential.
The status response supplies the address and the state of each viewer.
The **Wiki** tooltip names the Project title and the first 7 characters of the exported commit.
The **Traces** tooltip describes direct access to all traces in AgentsView.
Every AgentsView link provides automatic authentication.
The operator reaches the intended page without the AgentsView authentication page.
An access failure shows a Culler error state with its known cause and a retry control.

An unavailable viewer link has no address and stays in the focus order.
A click on an unavailable viewer link opens no tab.
Its tooltip states one of these reasons:

- No Project exists.
- The selected Project has no wiki export.
- The wiki address of the deployment is not ready. The tooltip names install.sh and redeploy.sh as the repair.
- AgentsView is unavailable. The tooltip adds the reason from the status response.
- The page awaits the status response or the Project list, or the read failed.

Each entry of the Project list has a wiki link with the same rules.
The wiki link is separate from the Project options.
The wiki link has no selected state. The arrow keys skip it.
The Tab key reaches the wiki link in the same way as the pause control.
The list closes after the wiki link opens the wiki.

The dashboard process serves the wiki export of each Project at a second address, the wiki address.
The wiki address requires no dashboard token.
The wiki address answers only below a secret path prefix, the wiki key.
Each wiki link holds the wiki key. Each other path gives 404.
Each export holds the wiki of the accepted commit of its Project.
A Project whose accepted commit has no `openwiki/` folder has no export.

A trace link opens the AgentsView session of one run at the last message of the run.
These lists show trace links:

- The recent runs of the Pipeline page
- The run history of the task dialog

A run without a main trace has no trace link.
A merge step has no trace link.
When AgentsView is unavailable, each trace link shows the unavailable state with the reason.
AgentsView requires the AgentsView token for its data.
A refused token makes AgentsView unavailable, and the reason states the refusal.

## Theme

The dashboard has a light theme and a dark theme.
The light theme shows navy text on porcelain paper and white surfaces.
The dark theme shows pale text on navy paper and dark blue surfaces.
Both themes use the Culler blue conventions.
Each page, each dialog, and each side panel uses the selected theme.

The header shows the theme button after the viewer links.
The theme button selects the other theme.
The browser preserves the selection across page changes and reloads.

A selection in one dashboard tab applies in each other dashboard tab of the same browser.
Without a selection, the dashboard uses the light theme.
The dashboard uses the dark theme only after the operator selects it.
The page shows the correct theme from its first paint.

The theme button is a toggle with the accessible name "Dark theme".
Its pressed state is true in the dark theme.
Its tooltip names the theme that a click selects.
The button shows a moon in the light theme and a sun in the dark theme.

Each theme uses its current background image.
Until the operator selects the background image, the page shows only the tile lines of the image.
The logo button shows and hides the egg, the nest, and the chick of the background image.
A click on the logo button moves each part of the background image by itself.
On a show, the large egg, the nest, and the three eggs come to their places one after the other.
Then a whole egg rolls to the place of the shell. Its shell cracks, its top comes off, and the chick comes out of the shell and jumps to its place.
On a hide, the chick jumps back into the shell, the top closes the shell, and each part leaves by itself.
A click during the motion plays the motion back from that moment.
The motion ends on the background image, and the background image stays under the theme veil.
A page load, a theme change, a change of the window size, and a selection in another dashboard tab show the state without motion.
With the reduced motion setting of the system, a click changes the state without motion.
The tile lines keep the same position in both states.
The logo button is a toggle with the accessible name "Background picture".
Its pressed state is true while the background image shows.
Its tooltip names the action of a click.
The browser keeps the selection, and each dashboard tab of the same browser applies it.
The theme veil preserves readable page content above the image.
The header keeps its navy band, its logo, and its light text in both themes.
The chick of the logo moves once each 10 seconds, moves while the pointer is on the logo, and moves during the motion of the background image.

These contrast requirements apply to both themes:

- Each text color has a contrast ratio of 4.5:1 or more against each surface.
- Each control text color has a contrast ratio of 4.5:1 or more against its actual surface.
- The focus ring has a contrast ratio of 3:1 or more against each surface.
- Each input border has a contrast ratio of 3:1 or more against its field and its surface.

Structural headers use the shared blue surface and white titles in both themes.
This rule applies to these components:

- Dialogs.
- Side panels.
- Named page panels.

Page titles stay on the page background.
Content titles use the text color of their surface.
Board columns and Pipeline stages retain their stage colors.

Dialog bodies and panel bodies use the shared body surface.
The shared subtle surface identifies these regions:

- Facts.
- Explanatory sections.
- Action footers.

Fields use the body surface and the shared control boundary.
Header controls retain a distinct readable surface.
The logo uses Fraunces.
Interface text uses Figtree.
Literal identifiers use the shared monospace font.

Primary actions use blue with white text in the light theme.
Primary actions use pale blue with dark text in the dark theme.
Each action group emphasizes at most one primary action.
Secondary actions use the body surface and the shared control boundary.
Dangerous actions use red text and an explicit action label.
Status colors retain these meanings:

- Blue identifies active work.
- Green identifies completion.
- Amber identifies attention.
- Red identifies failure.

Each status includes text or an accessible label.

Dialogs and bounded panels use a 16-pixel corner radius.
Fields and nested content use a 9-pixel corner radius.
Buttons use a 7-pixel corner radius.
Dialog regions share a 24-pixel horizontal inset, with consistent smaller insets on narrow screens.
Compact panels use a 16-pixel horizontal inset.
Dialog close controls show the shared cross and the accessible name **Close**.

Dialog close controls measure 36 pixels square with a precise pointer.
Actions measure at least 44 pixels square with a coarse pointer.

Dialog headers and action footers stay available while the body scrolls.
The body uses one vertical scroll region when the dialog exceeds the viewport height.
The shared backdrop covers the application navigation and page content.
A stacked dialog uses the shared lighter backdrop and retains visible context below it.

Keyboard focus stays within the active dialog and returns to the opener after closure.
Each dialog retains its cancellation protections and draft policy.
A theme change preserves open dialogs, the current view, and unsaved form values.
Controls remain accessible at 320 pixels wide and at 200 percent browser zoom.

Culler trace access pages use the same color roles and action appearance.
Their nonce-protected styles preserve the Content Security Policy and access controls.
A standalone trace page uses a system font fallback when its isolated origin cannot serve Figtree.
The dashboard-origin page follows the selected theme.
A separate-origin failure page follows its local selection or uses the light theme.

The Files changed view shows each diff in the colors of the selected theme.
A report frame shows its report on a light sheet in both themes.
A report can carry its own colors, and those colors assume a light sheet.
The log pane is dark in both themes.

The Setup Journal approval view retains its navy surface in both themes.
Source code, file comparisons, and agent artifacts retain the colors that their content requires.
Culler controls around specialized content use the shared conventions.
The OpenWiki viewer and AgentsView have their own appearance.

## System health

Every page shows current conditions in a persistent notice area.
In the narrow layout of the Review page, the open reader covers the notice area.
The notice area leaves the Project menu accessible.
The notice states the practical effect and provides the applicable repair action.
A single issue shows its name and action directly.
Multiple issues show one count and a **Review issues** control.
The issue dialog keeps each issue and its repair action separate.
A shared proposal decision counts as one issue, with links to its affected proposals.
The issue count excludes task blocks that the shared decision resolution clears.
Separate task blocks, exhausted task retries, and task allowance conditions remain separate issues.
The notice, issue dialog, and System health use the same issue count.
The decision text remains complete in the detail and resolution dialogs.
**View details** opens the repair dialog.
The dialog shows the cause, impact, retry state, and repair action.
For a model access failure, the dialog identifies the affected agent profile and model.
The dialog shows **Roles that use this profile** above **Technical details**.
The dialog states when Culler cannot confirm the cause or current status.
**Technical details** shows one dated diagnostic record without duplicate fields.
Connection failures show no retry counter or duplicate failure timestamps.
A separate history control opens diagnostic history on request.
The history lists completed results without separate start records.
Refresh activity appears only in history and shows one selected period.
The notice uses the dashboard surfaces, typography, and attention colors.
The notice preserves access to the page on narrow displays.
A routine status refresh preserves keyboard focus.
The status request uses the operational records that also control dispatch.
A complete response replaces the current conditions.
An incomplete response preserves known conditions and shows **Status unavailable**.
A status request sends no provider request.

The System section of Configs starts with System health.
System health shows three groups in this order:

- **Deployment checks** show the result and time of the latest inspection.
- **Connections** show current access and the applicable repair action.
- **Execution** shows task restrictions, failed Collie replies, Strategist restrictions, and AgentsView conditions. It also shows a merge of a wiki commit whose result Culler cannot prove, with the repair text of that merge as the repair action. When the repair text states only a condition, the repair action adds the delay before the next check of that merge and the operator instruction.

Resident availability appears beside the deployment checks.
Each connection uses one compact row.
Connections that require attention remain visible.
Connected and unused connections share one closed disclosure that states their counts.
An empty Execution group remains hidden.
Deployment checks and historical resident diagnostics remain in closed disclosures.
The deployment evidence opens when the inspection result changes to a failure.
The deployment summary excludes connection checks from the composed inspection.
Current resident availability remains separate from historical log failures.

The diagnostic dialog updates its current status after each complete status response.
An unavailable response labels the retained status as the last known status.
Recovery removes obsolete repair actions from an open dialog.
Recorded diagnostic details retain their failure timestamp after recovery.
The notice identifies the connection and agent profile.
A network failure shows the observed error and the earliest retry time.
An elapsed retry time shows **Retry pending** until an applicable attempt starts.
A profile connection in the retry state offers **Test model** throughout the retry delay and after the delay ends.
The control tests the saved profile through the model configuration test operation and shows its progress and result.
A successful test refreshes connection health. The test preserves Project pauses and other execution restrictions.
An active attempt shows its role, Project, and task or conversation.
The notice states that Culler awaits a complete model response.
An absent or stale heartbeat retains its separate execution status.
The details separate the earlier failure from the current attempt.
The details show the current attempt's start time and recovery state.
A fresh complete model response clears only the applicable network condition.
A brief confirmation states **The model responded**.
The confirmation states that the role continues only while its attempt remains active.
Repeated status responses preserve focus and produce one recovery announcement.
Connection history identifies the resolved failure and the attempt that confirmed recovery.
Connection recovery preserves separate execution restrictions and repair actions.
The retry time defines earliest eligibility. The resident retains its ordinary tick interval.

Only Configs runs the deployment inspection.
Configs runs the inspection when it opens, if no inspection started in the last minute.
Configs repeats the inspection each minute while it is visible.
**Re-check deployment** runs the inspection on request.
The button shows progress and retains earlier evidence if the inspection remains incomplete.
The inspection preserves connection allowances and execution allowances.
Current operational notices use the status response independently of the inspection result.
The resident availability display separates current availability from historical diagnostics.

The Board shows the selected Project's consecutive Strategist failures.
The third consecutive failure stops proposal runs for that Project.
**Resume Project infrastructure** requires a usable connection and preserves pipeline pauses.
The failed Collie reply retains its partial text and offers **Retry reply**.
An old or unavailable reply state displays an explicit status instead of an indefinite activity indicator.

Connection notices offer an applicable repair action:

- **Reconnect** opens the authorization flow for the affected connection.
- **Review connection** opens the affected agent profile and its connection.
- **Retry connection** authorizes another allowance for a safe transient failure.

A repair action without a dashboard control appears as text in the notice and in the issue dialog.

AgentsView conditions appear only in a deployment that prepared an AgentsView unit:

- AgentsView is unavailable. The condition states that trace links do not open and that runs and Collie continue.
- AgentsView did not import new trace lines in time. The condition names the count and the runs or Collie sessions.

The model test retains its explicit cost confirmation.
The diagnostic history remains available after recovery.
Connection details show refresh outcomes for the last 24 hours and the last seven days.

## Project configuration

The Board explains a Project when no active or paused project exists.
The empty Board shows **Add your first Project** and **Import an existing Project or create a new Project from scratch.**
The empty Board shows three steps: **Describe your vision**, **Agents work**, and **Stay in the loop**.
The steps remain visible.
Each review explains the changes and their reasons so the operator stays familiar with the Project.
When agent configuration requires attention, the empty Board shows one short setup message and a **Model configuration** action.
The message states that agent configuration requires attention before agents can start.
Agent configuration requires attention in these cases:

- A role has no agent profile.
- An assigned agent profile is disabled.
- The connection of an assigned agent profile has no usable access.

The setup message and Project start read the current configuration.
The form that starts a model test shows the result of that test.
The persistent system notice provides the connection failure summary on every Board.
The issue dialog and Model configuration provide the connection details.
The Board with an active or paused Project contains only Project content below the persistent system notice.

**Add Project** opens the folder and name form from the Board, Project selector, and Configs.
An empty folder and an imported repository use the same form.
The folder chooser opens Finder.
A typed path and a Finder selection receive automatic validation.
A valid path can end with a slash.
**Continue** registers one paused Project and opens its dedicated Vision page.
The form explains the Project access that Continue authorizes.
A failure preserves the folder and name.

**Open Vision** opens the selected Project's Vision page.
The page identifies the Project and its `VISION.md` destination.
Its address preserves that Project through a reload.
Its conversation stays associated with that Project independently of the global selector.
Ordinary Collie conversations remain separate.

The Vision page contains these controls:

- A Collie conversation with file uploads and persistent attachment access.
- A **Clear** action that ends the Collie conversation and starts an empty one. A cleared conversation cannot be restored.
- A model control in the conversation header. It shows the model and harness of the Collie conversation.
  The control opens a list of enabled agent profiles. A selection sets the Collie route for each new Collie conversation.
  A conversation keeps its agent profile. An empty conversation restarts with the selected agent profile.
  A conversation with messages offers a new conversation that uses the selected agent profile.
- An editor for the shared draft in Culler storage.
- A draft persistence status and conflict comparison.
- An explicit **Save Vision** action.
- A separate **Initialize project** action for a Project with incomplete initialization.

The page header holds the draft status, **Save Vision**, and **Initialize project**.
The explanation states that Culler prepares the Project, then starts development from its Vision.
Setup and development use agents and can make model requests.
The conversation and editor appear together on wide screens and fill the window height.
An empty conversation offers example messages. An empty draft shows a centered prompt.
Both remain accessible on narrow screens.
The editor remains available when Collie cannot reply.
Direct edits persist automatically without a workspace file change.
A conflict preserves local text and exposes the persistent text for comparison.
A failed save shows the workspace text and requires an explicit comparison before another submission.
A submission identifies the exact persistent draft revision.
The page distinguishes an accepted submission from a confirmed workspace file save.
A successful save confirms the destination and saved contents.
A new Project remains incomplete after Vision completion.
The page identifies incomplete initialization and the action that completes it.
**Initialize project** requires the current draft revision to match a confirmed workspace submission.
The action starts a separate setup phase. The setup report and source acceptance complete initialization and permit development.
A failed start preserves the draft and exposes the cause with a retry.
A successful start opens the Board with that Project selected.
The page distinguishes permission to begin work from an actual agent run.
The action becomes **View setup** while setup remains incomplete.
The start action disappears after initialization completes.
The Board provides **Open Vision** before setup starts.
After initialization authorization, the Board shows the full-page Setup Journal.
For initialized Projects, a Vision save preserves the current pause state.
The draft and conversation remain available after completion.

## Setup Journal

The Setup Journal fills the Board during initial setup.
It replaces the task columns, empty Board content, and Collie command bar.
The Project navigation and persistent system notice remain available.
The header identifies the Project and shows **Project setup**.
Normal progress uses neutral text and the state **Setup in progress**.
Alert styles identify approval requests and actual failures.
The Project pause text identifies the separate task development state.
The journal presents a read-only chat stream in one column.
The stream contains progress messages and milestones.
The page keeps action controls outside the stream.
Each report shows its summary with optional Markdown details in a collapsed section.

The stream shows these milestones as setup reaches them:

- **Vision accepted**.
- **Prepare Project environment**.
- **Save Project files**.
- **Ready for development**.

A milestone appears when its operation starts or when retained evidence proves completion.
Each milestone has one row at its first chronological position in the stream.
The row shows the latest state of that milestone.
Completion updates that row in place.
Another attempt resets the milestone until its own evidence proves completion.
Future milestones remain absent until setup reaches them.
Milestone rows and earlier messages remain visible.
The stream contains no composer or action controls.
The setup controls below the stream offer **Ask Collie** for the current setup failure.

The current operation shows its recorded status beside its message.
Its label identifies whether Culler or the setup agent performs it.
A setup agent operation with a registered trace shows a **Trace** link beside its status.
The link opens the AgentsView session of that operation in a new tab.
The link appears while the agent runs and remains after the operation ends.
An unavailable AgentsView keeps the link in place without an address and states the reason in its tooltip.

An activity indicator remains visible during each automatic setup stage.
The indicator covers these stages:

- The initial read.
- The queue.
- The interval between operations.

A queued operation retains its **Queued** label until execution starts.
Approval requests and execution failures show their required action.
Unavailable status and completion stop the activity indicator.
The header and the journal show the same setup activity state.
Reduced motion preserves the indicator and its text without animation.
The indicator gives no estimated percentage or inferred tool action.

An unavailable host observation stops activity between operations.
A current operation retains its recorded activity when a host observation fails.

Past operations retain their results and details after the next operation starts.
Retry attempts keep separate identities and appear in stable chronological order.
Each attempt retains a concise message in the stream.
Detailed output remains collapsed by default.
Image operations appear only when Culler records them.
Reports appear after Culler records their outcomes.

The journal distinguishes these outcomes:

- Successful completion.
- A proposal that requires approval.
- A complete report with environment limits.
- An execution failure.
- A canceled attempt.

An agent process exit alone completes no milestone.
Unknown outcomes show **Result unavailable**.
Missing historical records show **Evidence unavailable**.
Markdown details describe command results and environment limits.
Setup completion establishes preparation and import.
Repository requirements still control later task verification.
Absent timestamps remain absent.

Each failed operation shows its source error once with the shared Markdown viewer.
The controls below the stream contain the eligible **Retry setup** action and explain its purpose.
An automatic retry shows its recorded state and time.
**Open Environment** remains page navigation.
Setup health notices contain collapsed **Technical details** with redacted diagnostic evidence.
A failure without a source message uses a generic explanation.
An informational log remains diagnostic evidence.

The journal reads setup health through the host health endpoint independently of the spine.
The resident publishes observations after setup requests and result submissions.
The `mc` command determines freshness from the observation time and resident identity.
An observation becomes stale after 30 seconds.
The journal labels stale evidence **Setup status unavailable** and retains the last recorded error and its time.
A successful request clears its shared error.
A confirmed result clears only its submission error.
These observations preserve the recorded operation states and completion evidence.
A failed page refresh retains the last confirmed data.

### Text and files

Explanatory prose uses Collie's shared Markdown viewer.
This includes operation summaries, proposal explanations, and prose within expanded details.
The viewer supports these elements:

- Paragraphs and section titles.
- Bulleted and numbered lists.
- Bold and italic text.
- Inline code and fenced code blocks.
- Block quotes and links.

Plain text remains readable, and unsupported syntax remains literal text.
The journal keeps progress states and actions outside the Markdown content.
Paragraphs use a maximum width between 60 and 80 characters and a line height of at least 1.5.
Section titles, paragraphs, and lists have distinct spacing.
Text surfaces provide readable contrast in light and dark themes.
Long paths and links wrap within the content area.
Wide raw command output scrolls horizontally within its region.
The page remains usable in a narrow window and at 200 percent browser zoom.
The keyboard reaches each action and expandable result.
State text remains clear without color or motion.
Motion respects the browser's reduced-motion preference.

The file viewer shows Dockerfiles, required build files, and setup scripts.
Raw command output uses literal code.
These views preserve exact recorded contents, including Markdown build files.
Embedded Hypertext Markup Language (HTML) remains literal text.
Markdown cannot execute scripts, create application controls, or fetch embedded remote content.
The Markdown viewer rejects links that execute code or invoke application commands.

### Image dependencies

Workspace dependencies use ordinary permissions under the initial setup authorization.
Image dependencies require approval of an exact Dockerfile and its required build files.
An initial proposal changes the image inputs.
A setup script or another configuration change without an image change requires no approval.
The stream identifies each proposal that requires approval.
Its stream message refers the operator to the suggested setup.
The controls below the stream present the approval view, **Suggested setup**, with these items:

- **Edit Dockerfile** when the proposal has a Dockerfile. It opens the Environment page with the focus in the Dockerfile editor.
- A statement of purpose: each agent of the Project starts in a container with this setup, so that it can change the repository and run its tests.
- The **Project image** part:
  - At most two short sentences about the image change and its purpose, directly above the Dockerfile.
  - The Dockerfile in a file viewer.
  - Each required build file in a file viewer within collapsed details. The details summary states the change type and the path.
  - The build arguments, when present.
- The **Setup script** part, when the proposal or the accepted configuration has a setup script:
  - A statement that Culler runs the script in each agent container before the agent starts.
  - The setup script in a file viewer. Its file header names the Bash language.
    Each version of the script ends with one line break, so a final line break alone is no change.
- **Approve changes and continue** for the displayed revision.

The file viewers use recorded proposal contents rather than mutable workspace files.
Dependency configuration remains available on the Environment page.
A long retained explanation appears under **Proposal details**, collapsed by default.
Its main summary uses a neutral description of the image changes.
Approval prepares the image and starts a continuation after Culler confirms runtime compatibility.
**Continue with current image** records denial and starts a continuation in the current image.
Either initial continuation must submit `complete` with accurate limits.
The report permits automatic source acceptance.
The journal shows the operator's decision.
Further image improvements use the Environment page after import.
An approved historical proposal cannot request approval again.

### File viewer

The file viewer compares the proposed contents of a file with the accepted contents of the same path.
The file viewer uses the local Pierre Diffs modules in the unified layout.
Each row shows one source line and its line number.
A rule separates the line numbers from the code.
An added line has the added-line background and a solid bar at its line number.
A removed line has the removed-line background and a striped bar at its line number.
Unchanged lines use the code background and have no bar.

The file viewer in the approval view uses the dark theme colors in both themes.
Its added-line and removed-line colors match the Files changed view in the dark theme.

The file viewer shows each change type in this way:

- A new file shows each line as added.
- A removed file shows each line as removed.
- A changed file shows its changed lines with unchanged context lines. A separator states the number of unchanged lines that it replaces.
- An unchanged file shows its contents with line numbers and no marks.

**Show complete file** shows the complete proposed contents of a changed file.
**Show changes** then shows the comparison again.
The file header states the path and the change type in text: Added, Changed, Removed, or Unchanged.
The file header of a comparison also states the number of added lines and removed lines.

Long lines wrap within the code region.
Each source line has one line number.
The file viewer has no horizontal scroll.
A view with more than 20 rows opens collapsed at its first 20 rows.
**Expand** below the code shows the complete view in the page, without an inner scroll region.
An expanded view shows **Collapse**.

A refresh keeps the collapse state and the view of each file viewer.
A copy of selected lines yields only the file text, without line numbers.
The file viewer is for inspection only.
When the Pierre Diffs modules fail to load, the file viewer shows the exact proposed contents as literal code.
A removed file then shows its exact accepted contents.
The file viewer then states that the comparison is unavailable and shows **Retry**.
The approval actions remain available.

The approval view is a navy panel with the dark theme colors in both themes.
It has these colors:

- The approval fill differs from the journal surface.
- The approval boundary has a contrast ratio of 3:1 or more against the journal surface.
- The file header fill differs from the approval fill.
- The code region boundary has a contrast ratio of 3:1 or more against the approval fill.
- Text has a contrast ratio of 4.5:1 or more against its background.
- Line numbers and syntax colors have a contrast ratio of 4.5:1 or more against each line background.

A historical proposal uses the same file viewer with the colors of the selected theme.
Only the approval view uses the navy panel.

### Recovery and completion

Each action identifies the exact Project, operation, or proposal revision that it affects.
Repeated actions create no duplicate work.
Stale approvals require the current proposal before acceptance.
A failed action preserves the journal and shows its cause beside that action.
Page visits and refreshes perform reads only.
Current setup state determines the available approval, retry, and Environment actions.

A reload reconstructs history from retained records.
Refreshes preserve focus, scroll position, and expanded details.
A Project change cannot display another Project's journal or accept its late responses.
A failed read preserves the last confirmed journal and its last successful read time.
The activity indicator stops while state is unavailable.
A first read failure shows **Setup status unavailable** and a retry action.
Read failures remain separate from recorded setup failures.

The first successful file acceptance with `initial_setup` completes the initial setup history.
The journal shows the completion summary and time.
Later environment changes preserve this history.
**Ready for development** requires accepted environment files and complete Project initialization.
An open journal retains its completion summary and offers **Open Board**.
The Environment page provides **Setup history** after completion.
A reload of a completed Project opens the normal Board.
The journal never delays authorized development.

## Project environment

Each Project on Configs has an **Environment** action.
The dedicated page address retains the Project through a reload.
Automatic setup prepares the configuration. The page remains optional during initial setup.
The page shows its content in one column.

### Status

The page shows one status for the Project environment.
The status distinguishes active work, a required approval, a failure, and readiness.
A proposal from an agent shows its explanation in the status.
A ready Project uses its accepted environment until Culler accepts the draft.
The page reports runtime incompatibility separately from the agent's report.
A failed setup and a runtime incompatibility provide **Retry**.

The status shows the newest report of the draft.
The accepted environment supplies the report when the draft has no report.
The summary remains visible.
Optional Markdown details remain collapsed until the operator opens them.
The report retains the actual command results and environment limits.
These observations leave import authorization intact.

### Configuration

The page provides these editors:

- A standard Dockerfile and its repository path.
- A Bash setup script.
- Nonsecret variables, with one `NAME=value` line for each variable.

Each editor shows line numbers and the exact recorded text.
A Project without a Dockerfile shows **Add Dockerfile**.
A variable keeps its recorded execution scope.
A new variable applies to the setup script and to each agent run.

**Advanced** stays closed until the operator opens it.
Its title shows the count of each configured item.
**Advanced** provides these fields:

- The base image digest.
- Services with their start and readiness commands.
- Secret references with their execution scopes.
- The referenced build files and nonsecret build arguments.
- The setup directory.
- The package declaration paths.

### Actions

Unsaved edits remain in the browser until the operator applies or discards them.
**Save and apply** saves the edits as the draft and approves that exact draft.
**Apply** approves a saved draft that awaits approval.
The approval includes the required image build.
The complete report and source acceptance apply the draft automatically.
The page explains this effect beside the action.
Culler prepares an operator draft against the accepted commit.
A task proposal keeps its source commit.
The apply action stays unavailable during an active environment operation.
**Discard** restores the draft that Culler holds.
**Ask agent to configure** accepts a description and requests a draft from the setup agent.
**Setup history** opens the Setup Journal of the Project on the Board.
A conflict preserves browser edits and provides the saved draft for comparison.
A Project change preserves unsaved edits for their original Project.
Archived Projects provide read access only.
A refresh preserves focus, typed text, and open details.

The page shows the measured image and cache sizes.
An absent measurement remains unavailable.
**Clear Project cache** preserves the Project files and the accepted environment.

## Board

The normal Board includes a Collie command bar below the task columns.
The command bar remains visible near the bottom of the window as the operator scrolls.
The selected Project supplies the conversation context.
An empty registry uses an all-Projects context.
Each context keeps a separate ordinary Collie session and message draft.

The first message creates the session when the browser has no saved session identifier.
The browser retains the session identifier across reloads.
An absent saved session starts a new conversation automatically.
A connection failure preserves the saved session for another attempt.

Send opens a response panel above the command bar and submits the message through the ordinary Collie interface.
The panel shows message history, live reply text, and reply status.
Collie previews a new state change in the conversation and requests confirmation before execution.
Explicit confirmation authorizes only the described change.
The interface uses real Collie replies without keyword substitution or sample responses.
The Board refreshes after a reply completes.

Hide conversation preserves the session and the message draft.
The header provides New conversation, including while Collie prepares a reply.
The Conversation actions menu contains Open in Collie.
Open in Collie selects the same session on the Collie page.
New conversation starts a session and preserves the message draft and attachments.
The previous session remains in the Collie list.
A failed submission preserves its text and attachments for another attempt.
The command bar remains available after the session ends.
Send resumes the same session automatically and preserves its conversation history.
The session can expire while the Board remains open.
A failed reply exposes the ordinary Retry reply action.

The empty input cycles through three suggestions every four seconds, with “What needs my attention?” first.
Each transition slides and fades the text over 420 milliseconds.
The reduced-motion preference removes the animation.
The cycle pauses while the input has focus, contains text, or remains unavailable.
The command bar supports keyboard input and both dashboard themes.

The Board shows the selected Project's live tasks in lifecycle columns:

- Proposed
- Seeded
- Worked
- Verified
- Packaged

The task counts and lease indicators use the selected Project.

The stats row above the columns shows each count as a number with a sentence-case caption below it:

- **Blocked tasks**: tasks that await an operator decision. Tasks can share one decision. A nonzero count has the alert treatment.
- **Agent runs**: the agent runs that hold a Project lease.
- **Tasks**: the live tasks.
- **Stalled**: stalled tasks.
- **Expedited**: expedited tasks. This stat appears only when its count is above zero.

Blocked tasks and Agent runs lead the row with more weight. A stat with a zero count recedes.
The Review and Configs stats use the same number-first layout and sentence-case labels.

The Board provides a Create task form for tasks.
The form shows the destination Project and requires a title.
Priority 2 is the default, and Expedited represents priority -1.
The Collie link opens the Collie tab and sends no message.
The command accepts new user records at Seeded after it validates their repository assignments.
A successful creation opens the new record.
A failed submission preserves all form values and the command error.
An exact retry preserves the operation identifier, including after a page reload.

An undecided record provides these controls:

- Edit title and priority
- Revise requirements
- Require decision with a required reason
- Resolve decision
- Cancel with a required reason

Each form submits the record version that the operator inspected.
A saved form refreshes the open task dialog. A task dialog that the operator closes during that refresh stays closed.
A conflict displays the current record and preserves the operator's unsaved values.
The operator can adopt the current record version before another submission.
The forms expose protected record fields as information only.
Approved, rejected, cancelled, and archived records expose no content edits.
An approved, unarchived record permits Resolve decision when a block or infrastructure stall prevents its merge.

The requirements form submits revised requirements and a required reason.
The form accepts a save only with a changed description and a reason.
The dialog distinguishes effective requirements from requested requirements.
A revision that waits for an active run identifies that run and explains the wait.
Edit and revision history preserves prior values, requested values, reasons, actors, times, and outcomes.

Each Decision Pending condition has a condition type.
The condition type selects the controls that resolve the condition:

- **Operator decision report**: a Worker, Verifier, or Packager reports a decision that only the operator can make. Resolve decision opens the decision form.
- **Customization conflict**: the operator edits the role customization. Then Resolve decision opens the reason form.
- **Correction allowance limit**: **Authorize three more corrections**, Revise requirements, or Cancel task resolves the condition. The record offers no Resolve decision.
- **Confinement refusal**: the operator changes the launch configuration. Then Resolve decision opens the reason form.
- **Operator checkout condition**: the operator repairs the operator checkout. Then Resolve decision opens the reason form.
- **Operator block**: Require decision sets the condition. Resolve decision opens the reason form.
- **Proposal decision**: **Resolve proposal decision** in the notice area of System health resolves the condition. The task dialog links to the shared decision. The card states **Awaits shared proposal decision** for a task with a proposal block. Resolve decision stays unavailable on the task.

The decision form shows these items:

- The report of the agent
- The current description, open for edits
- The reason field "What resolved the decision?"
- The statement "The Worker continues from the last commit of the task."
- **Cancel task** as the other resolution. It opens the cancellation form in the same dialog.

The decision form accepts a save only with a changed description and a reason.
The form states what the save still needs.
The save submits a requirements revision. The revision ends Decision Pending and returns the task to Seeded.
A revision that waits for an active run or a repository operation keeps Decision Pending.
The record controls then show the requested requirements and the wait status.
Resolve decision stays unavailable until the revision applies.

The reason form shows the reason field "What resolved the decision?" only.
The reason form keeps the description.

A Project action card near the Board top identifies every Project that requires a valid `VISION.md`.
The card names the Project and states the action that starts work.
**Open Vision** opens that Project's Vision page.
The card states how Culler uses the Vision to plan work.
A detail control shows these facts:

- The required file path
- The reason work cannot start

The selected Project also shows its notice on the other project pages.
The normal refresh checks the current file status and removes resolved notices.

Empty columns remain visible and explain which tasks belong there.
Each column shows its task count.
Each stage uses the same accent color on the Board and in the task dialog.
Each task card shows its identifier and its time in the current stage when that time is available.
The whole task card opens the task dialog and supports keyboard focus.

Each task card opens a task dialog with these facts:

- The task description, stage, and decision
- The active run, heartbeat, and recent run history
- The blocked reason, infrastructure failures, and available failure evidence
- The Verifier evidence of the latest packet
- The link to the review packet

The task dialog of a task that the Strategist proposed shows the section "Strategic rationale" as its first content section, above the description.
The section shows the rationale of the Strategist through the dashboard Markdown renderer.
The task dialog of a task that the operator defined shows no "Strategic rationale" section.

Each run row shows the result, the role, the age, and the trace link of a run with a main trace.

The task dialog shows the file name of the Verifier evidence.
A click on the Verifier evidence copies the host path of the file.
The host path comes from the artifact roots that the page reads.
A path without a host root stays as the record states it.

The review packet of a task holds the report of that task.
The task dialog shows the link to the review packet only when the Review page lists that packet.
A task dialog on the Review page shows no link to the review packet.
An open report keeps its read position during a refresh.
The task dialog shows commit identifiers only in its edit and revision history.

The card of an approved task shows "Approved" in place of "Review packet available".
The task dialog of an approved task shows the merge state text.
The link to the review packet opens the Review reader for that packet directly.
Each report frame of the dialog follows the link rules of the Review report area.

The dialog preserves its position during a refresh.
The close button, Escape key, and backdrop close the dialog.
A task outside the current list cannot retain an open dialog.
A final run with a retained lease shows Stopping.
A final run adds zero to the active task count.
The Board preserves task stages during infrastructure failures.

The task dialog shows an alert only for a condition that stops the task now or that needs an operator action now.
Each state chip in the dialog head has text with a contrast ratio of at least 4.5 to 1 in each theme.
Stalled tasks state that three infrastructure failures used the retry allowance.
The alert of a stalled task shows the observed condition beside **View run diagnostics**.
The cause remains unknown unless the report supplies evidence for a cause.
A disclosure contains the failure evidence and report source.

A resolved infrastructure failure appears only in its run row and in its run diagnostics.
Each run row states the result in plain words. Internal reason codes appear only in run diagnostics.
A Verifier run that requests a correction states **Requested a correction** in its run row.
The correction result uses a neutral tone unless the run also records a failure.
The task history retains the correction request.
The task dialog shows the Verifier report only in the alert for the Verifier correction limit.
The side column of the task dialog shows the count of unsuccessful Verifier corrections.
Each run in the task history links to its diagnostics.
Each run with a main trace in the task history also has a trace link.
Blocked tasks show the decision or repair that permits work to continue.
A merge step that blocks a task shows its reason as the block reason.
The task cards and the task dialog show no repository operation as a run.
A task card and a task dialog show no Scribe step.

When a connection check stops the next run of a task, the card and the dialog name the connection.
Culler starts the run when the connection is available.

The Board acknowledges blocked alerts only for blocked tasks that it displays.
An alert for another Project remains undelivered.

## Collie

**Ask Collie** opens an ordinary conversation on the Collie page and submits the selected diagnostic issue.
These views provide the action:

- The system notice and issue dialogs.
- System health conditions that require attention.
- Deployment failures and unavailable resident services.
- Task blocks, stalls, stale heartbeats, and connection restrictions.
- Failed setup operations and environment conditions that require repair.
- The diagnostics of a selected run.

The message contains the issue summary and relevant record identifiers.
The message limits long evidence to a labeled excerpt.
Full reports and complete logs remain outside the initial message.
Collie uses its permitted reads to retrieve further evidence when needed.
The message identifies an unavailable current status as the last known status.

The action preserves the existing conversation and composer draft.
The action shows progress and preserves the issue after a failed request.
A retry checks the conversation history before another message after an uncertain message response.
The conversation requests an explanation and proposed action.
A state change requires explicit operator confirmation.

Collie displays active and ended conversations across all surfaces.
A packet conversation from the quick chat of the Review page appears in the list after its first message.
The row of a packet conversation shows the task number of its packet, for example "Task #7".
The operator can continue a packet conversation on this page with the same packet context.
A message from this page shows in the quick chat at the next refresh.
A message from the quick chat shows on this page at the next refresh.
After the operator deletes the current conversation of a packet, the quick chat of that packet shows the empty state.
A packet conversation of an archived Project accepts no message. The page shows the cause.
A Project selection has no effect on a conversation.
Project pipeline pauses leave Collie available.

The page preserves the selected conversation.
A page open selects the conversation with the most recent activity when no conversation is selected.
A page open resumes the selected conversation if it ended.
An empty session list creates one conversation when the page opens.
Concurrent page opens reuse the same conversation.
Preparation starts an idle container. The first message starts a model request.
An example message fills the composer. It sends no message.

A conversation retains its permanent identifier and a separate display name.
An empty conversation shows **New conversation**. Its first message supplies a short excerpt until Collie sets a title.
The excerpt contains at most six words and 40 Unicode code points, including the ellipsis.
An attachment-only message shows **Conversation with attachments** until Collie sets a title.

Collie sets a concise title through its session command during the first response.
The title requires no separate model request.
The command preserves an existing title. A failure leaves the excerpt available and does not stop the response.

Sessions without title permission retain the excerpt until the operator names them.
**Rename conversation** accepts one line with 1 to 100 characters and preserves the conversation identifier and history.
Names persist across page reloads and session restarts. The session list renders each name as plain text.

Each conversation offers an ellipsis menu and the same menu through a right click.
**Delete conversation** requires confirmation and stops replies from that conversation.
Deletion removes the conversation from the list across browsers and prevents a resume.
Culler retains internal records and files for references and container cleanup.
The page selects another conversation after deletion of the selected conversation.
The page creates one conversation when deletion removes the last conversation.
A periodic refresh starts no conversation and resumes no conversation.

The page shares its conversation controls and visual styles with the Vision page.
The session list replaces the Vision editor.
The conversation header contains **New session**.
The session list and transcript scroll independently within the window.
The page header, conversation header, and composer remain visible.
The conversation header shows the model and harness.
The model control sets the agent profile for new conversations.
The model control is unavailable for a packet conversation. The quick chat of the Review page selects the model of a packet conversation.
A model selection replaces an empty conversation and stops its previous container.
For a conversation with messages, an in-app confirmation dialog names the selected model and explains that it starts a new conversation.
The dialog states that the current conversation stays in the session list.
Cancel preserves the current conversation, model, draft, and attachments.
A selection in a conversation with messages opens a new conversation and preserves the previous conversation.
The selector explains this effect before the selection.
The page prevents model changes during a message submission or an active reply.
A model change preserves draft text and attachments.
A concurrent message prevents removal of its conversation.
A retry reuses the same model request.
Both Collie and Vision accept image paste and file attachments through the same composer.
The composer shows each attachment with a remove control and each image with a preview.
Text paste preserves normal text input behavior. A paste sends no message.
The attachment total must not exceed 16 mebibytes per message.
**Enter** sends the message. **Shift+Enter** adds a line.
A failed send preserves the message and its selected files.
The session list and conversation remain accessible on narrow screens.

An active conversation with an unanswered message refreshes each second.
Other conversation states refresh every ten seconds.
A provisional draft appears until the final reply arrives.
An ended conversation permits an explicit resume.
Typed attachment claims use the protected attachment endpoint.
Allowlisted raster attachments appear inline. Other attachments use download links.

## Review

The Review page shows the latest packet of each task in the selected Project.
A packet is the review record of one submitted task version.
A review packet shows no Scribe step.
`mc packet list` states one review state for each packet.
The page has two views: "Live queue" and "Ledger".
One segmented control at the top of the queue column selects the view.
A packet that a requirements revision replaced appears in neither view.

The Live queue is the default view. It shows the packets in these review states:

- Awaits decision: the packet waits for the operator decision.
- Approved: the operator approved the packet, and the merge is not complete. The row shows the merge state text under the task title.
- Returned to the pipeline: the task went back to the pipeline after the review. The row states the recorded cause.

Packets that await a decision come first. Each group uses oldest-first order.
A row shows the task number, the age in the queue, and the task title.
A row for a blocked task shows "Blocked" and the block reason.

The view shows the number of packets that await a decision.
The view also shows the review slots in use and the review capacity of the Project.
The Review tab badge counts the packets that await a decision.

The Ledger shows the packets with a final decision: Merged and Cancelled.
The newest decision comes first. For an equal decision time, the higher task number comes first.
A Ledger row shows the task number, the task title, the decision, and the local date and time of the decision.
A merged row names the destination branch. A cancelled row shows the cancel reason.

The Ledger filter has three options: "Approved", "Cancelled", and "All".
The filter appears only in the Ledger view, directly above the packet list, with the label "Show".
"Approved" shows the merged packets. Each page load selects "Approved".
The filter keeps its selection across data refreshes, view changes, and Project changes.
Each filter option shows the number of packets that it selects.

The packet list is the list of packets in the current view.
The reader is the part of the Review page that shows one packet.
The open packet is the packet that the reader shows.

### Evaluation cases

- The Development flag defaults to false and takes effect without a restart.
- An enabled flag exposes **Score case** below the report in the Live queue and the Ledger.
- The control remains outside the report frame at wide and narrow widths.
- An incomplete capture names its absent source or evidence and prevents an assessment submission.
- The score dialog identifies the exact task, packet version, and reviewed commit.
- The dialog preserves the report sandbox and the Content Security Policy.
- The dialog supplies the exact rubric snapshot, its guidance, and its score anchors.
- Each initial score remains blank until the operator selects a value.
- The assessment requires six integer scores from zero through four and an overall reason.
- The assessment requires an explicit critical failure determination and the familiar Project context.
- Each critical failure requires its category, explanation, and packet or source evidence reference.
- The source inspection and evidence inspection records describe operator review, rather than additional verification.
- The draft survives dialog closure and a failed submission.
- The packet decision remains separate from the assessment submission.
- **Case saved** appears only after a durable receipt identifies the exact packet and assessment revision.
- An authenticated download preserves the source bundle and each assessment revision.
- A disabled flag preserves authenticated access to saved cases and refuses new assessments.

### Wide layout

The wide layout applies at a window width of 960 px and more.
The Review page then has one panel with these parts, from left to right:

1. The queue column, with a width of 264 px
2. The reader, which fills the remaining width
3. The chat column, with a default width of 400 px, while the quick chat is open

The reader gives its width to the chat column.
While the chat column is open, the queue column has a width of 200 px.
When the reader would be narrower than 640 px, the queue column hides while the chat column is open.
The queue column comes back when the chat column closes or when the window has space for it.

The divider between the reader and the chat column controls the chat width.
A drag to the left expands the chat column and reduces the space for the reader.
The queue rule uses the selected chat width.
The chat column has a minimum width of 320 px.
The reader keeps a width of at least 400 px after a resize.
The divider supports these controls:

- A drag changes the width immediately.
- The Left and Right arrow keys change the width by 24 px.
- The Home key selects the minimum width.
- The End key selects the maximum width.
- A double-click restores the default width.

The browser keeps the selected width across packets, pages, and reloads.
A smaller window limits the visible width and preserves the selected width for a larger window.
The narrow layout hides the divider.
A resize preserves the packet, the conversation, and the draft text.

The queue column holds the view control, the counts, the Ledger filter, and the packet list.
The task title of a packet row shows a maximum of 3 lines.
The row of the open packet uses the dashboard accent color and a light accent background.
A left bar and an accessible current state also identify the open packet.
When the reader moves to another packet, the packet list scrolls the row of that packet into view.

When the window height is 600 px or more, the panel fills the window height below the notice area.
The panel then has a minimum height of 320 px.
The page itself does not scroll while the window has space for that minimum.
The packet list scrolls inside the queue column.
In a window with a height of less than 600 px, the panel has a height of at least 520 px, and the page scrolls.
The header, the tabs, and the notice area stay in view and stay available.
Page errors show in the page error area, as on each other page.

With no open packet, the reader place states "Select a packet to read its report."
With an empty packet list, the reader place shows the empty text of the list.
Before the page reads the packet list, the reader place states the state of the read.
The queue column then shows no empty text.

### Narrow layout

The narrow layout applies at a window width of less than 960 px.
The queue column then uses the full width of the panel.
A click on a packet row opens the reader over the full browser window.
The reader covers the dashboard header, the tabs, the notice area, and the queue column.
Each other part of the page is then inert, and the reader shows the page errors.

The reader bar then starts with a close control. The close control closes the reader.
The close control names the view: "Review Queue" or "Review Ledger".
In the Live queue view, the close control shows the number of packets that await a decision.

At a window width of 390 px, the reader fits the window width.
Below a window width of 600 px, these rules apply:

- A panel covers the full content area.
- The Files changed view scrolls as one column. The file tree is above the diff, and the file header stays at the top.
- The decision controls stay visible at the bottom of the window.
- Each control of the reader is at least 40 px tall.

A change of the window width moves the Review page between the two layouts and keeps the open packet.

### Reader layout

The report frame uses an off-white page background (`#fdfdfe`).
The page background remains the same when a report supplies its own document styles.
The dashboard supplies the base typography.
Report styles can arrange visual content within the page.
The Packager directive defines the default packet sections, which Project instructions can change.
The frame preserves the report content and its section order.

The reader has four parts, from top to bottom:

1. The reader bar
2. The view row
3. The content area
4. The decision bar

The reader bar, the view row, and the decision bar stay in a fixed position.
The content area shows one view at a time: the Packet view, the Files changed view, or the Collie view of the narrow layout.
The report area is the content area of the Packet view.
Only the content area scrolls.
Each error of a reader action appears inside the reader.

### Address and history

The browser address names the open packet, for example `#review/7`.
The address `#review` names no open packet. This address is the list entry of the Review page.
A reload opens the reader for the same packet.
An address that names a task without a packet in the selected Project shows the list entry.
A link from another page, such as the Board task dialog, opens the reader for its packet directly.

In the wide layout, the Review page opens the first packet of the packet list in these cases:

- The operator opens the Review page, and the address names no packet.
- The operator selects another Project on the Review page.
- The operator changes the view or the Ledger filter, and the new packet list does not hold the open packet.

The page opens that packet after it reads the packet list.
When the new packet list is empty, the reader shows no packet.
In the narrow layout, the Review page opens a packet only when the operator selects it.

When no packet is open, a click on a packet row adds one browser-history entry for that packet over the list entry.
The first packet that the page opens adds one browser-history entry in the same way.

A click on the row of another packet, the previous control, and the next control move the reader to that packet.
These moves replace the browser-history entry of the open packet.
When the list entry is directly below that entry, these moves also remove the steps of the report frame.
One Browser Back then shows the list entry.

Browser Back from the entry of a packet shows the list entry, with no open packet.
The page opens no packet automatically at a list entry that Browser Back shows.

Each click on an in-page link of a Hypertext Markup Language (HTML) report adds one step to the browser history.
Browser Back first moves the report back through these steps inside the frame.

A reader that opens from an address, such as a reload or a link, adds no browser-history entry.
The close control of the narrow layout returns to the list entry when the list entry of the same page load is directly below.
In each other case, the close control replaces the address with `#review`.

### Keyboard focus

In the wide layout, a click on a packet row keeps the keyboard focus on the row.
The first packet that the page opens does not move the keyboard focus.
When a control of the reader becomes unavailable while it has the focus, the focus moves to the reader.

In the narrow layout, the keyboard focus moves into the reader when the reader opens.
When the reader closes, the keyboard focus moves to the row of the packet.
When that row is absent, the keyboard focus moves to the packet list.

### Reader bar

In the wide layout, the reader bar holds these items, from left to right:

- The task number and the task title
- The tags of the packet
- The "Task" control
- The previous control, the position of the packet, for example "2 of 5", and the next control

The "Task" control and the previous and next controls stay together at the right end of the reader bar.
When the reader bar needs two rows, these controls move to the second row together.

In the narrow layout, the reader bar holds the close control, the previous and next controls with the position, the task number and the title, and the tags.
The "Task" control then stays at the end of the view row.
The "Collie" view control of the view row then replaces the "Ask Collie" control.

A long task title ends with an ellipsis. A pointer hover shows the full title.
The tags show the decision time of a packet with a decision, the review state, and "Decision Pending" for a blocked task.
A packet that awaits a decision has no review state tag.

The previous and next controls follow the order of the current packet list and the current Ledger filter.
The previous control is unavailable at the first packet. The next control is unavailable at the last packet.
When the open packet is absent from the current packet list, both controls are unavailable.
The position of the packet is then absent.

The "Task" control opens the task dialog of the task of the packet, over the reader.
The control is unavailable while the page sends a decision.
A failed read of the task appears as an error of a reader action.
The Review page keeps the open task dialog in step with its data refresh.
When the task dialog closes, the keyboard focus returns to the "Task" control.

### View row

The view row is one row below the reader bar.
In the wide layout, it holds two view controls: "Packet" and "Files changed".
In the narrow layout, it holds a third view control: "Collie".
The Packet view is the default view.
The "Files changed" label states the number of changed files of the packet, for example "Files changed · 12".
While the Files changed view shows its files, the view row also holds the display controls of that view.
The display controls are the file tree control and the diff layout controls.
The view row holds no commit and no other Git record.

The view controls follow the tab pattern. The arrow keys, Home, and End select a view.
Each view keeps its scroll position and its selected file while the operator uses the other view of the same packet.
Another packet or another Project opens the Packet view.
In the narrow layout, an open quick chat keeps the Collie view.

### Files changed

The Files changed view shows the exact Git changes of the packet.
The repository component of Culler derives the change data from Git. The page reads the change data through `mc`.
The change data identifies its exact commits and its packet version.
The page reads the change data when the operator first opens the view for a packet.
The page discards each response for a packet that the reader no longer shows.

The view shows one comparison: the Git range from the destination commit through the merge candidate commit.
The range holds the changes that the merge adds to the destination branch.
The submitted version of the task holds the verified Worker commit.
The range thus holds the source change of the task only.
The view shows the change data of the current merge candidate of the packet version.
Without that change data, the view shows the change data of the merge candidate that the packet records.

The file tree column holds the file tree between two parts:

- Above the file tree: the number of changed files and the numbers of added and removed lines.
- Below the file tree: the commits of the comparison.

The commits of the comparison are these:

- the destination commit, with the destination branch;
- the merge candidate commit;
- the verified Worker commit.

When the view shows a statement instead of the file tree, the commits show below that statement.
Each commit shows its first 7 characters. A pointer hover shows the full commit identifier.
A click on a commit copies the full commit identifier.

The diff starts directly below the view row.
The "Hide the file tree" control hides the file tree column. The diff then fills the width of the view.
The "Show the file tree" control shows the file tree column again.
The browser preserves this choice across packets and reloads.

The file tree lists every changed path of the comparison, with incidental and generated files.
Each tree row shows the change type and the numbers of added and removed lines.
A search field filters the tree.
A click on a file or Enter on a file shows the diff of that file.
The view first shows the first file of the tree.
The previous and next controls move through the files in the order of the tree.

The file header names the path, the change type, the line counts, and the position of the file, for example "2 of 7".
The diff shows line numbers and syntax colors.
The "Unified" and "Side by side" controls select the diff layout for each file.

The diff shows each change with 3 unmodified lines around it.
In a modified, renamed, or copied text file, each region of other unmodified lines has an expand control.
The expand control shows the lines of that region.
At the first expansion of a file, the page reads the version of the file at each commit of the comparison from the Project repository.
The diff keeps the exact Git hunks of the patch.
A failed read states its cause below the file header. The next expansion reads the file versions again.

Each change without a text diff states its cause:

- A rename or a copy states the previous path and the similarity.
- A mode change states the previous mode and the new mode.
- A type change states the previous type and the new type.
- A binary change states that Git treats the file as binary.
- An empty new file, an empty deleted file, and an unchanged content each have their own statement.

A diff with more than 2000 changed lines or more than 256 KiB waits for the operator.
The view then states the size of the diff. The "Show the diff" control shows the complete diff.

The view states the cause when it cannot show the comparison:

- For expired change data, the view states the expiry date. The repository keeps the commits.
- For a packet without a merge candidate, the view states that no merge candidate is available.
- For a merge candidate without recorded change data, the view states that Culler recorded no change data.

A failed read states its error and shows a "Retry" control.
The dashboard serves each module of the viewer from the local deployment, at a pinned version, with its license notice.
The view loads nothing from outside the local deployment.
The view shows repository text as display content only.

### Quick chat

The quick chat holds one Collie conversation about the open packet.
The reader offers the quick chat for each open packet, in the Live queue view and the Ledger view.
The quick chat is closed by default.
The browser keeps the open or closed state across packets, pages, and reloads.

In the wide layout, the quick chat fills the chat column.
The chat column has the full height of the Review panel.
The composer of the quick chat is level with the decision bar.
In the chat column, the message field and the Send control share one row at the height of the decision buttons.
Each part of the reader stays fully visible while the chat column is open.
The text at the top of the content area stays in view when the chat column opens or closes.

In the narrow layout, the quick chat fills the Collie view.
A selection of "Collie" opens the quick chat. A selection of "Packet" or "Files changed" closes it.
The reader bar, the view row, and the decision bar stay visible in the Collie view.
The Packet view and the Files changed view keep their scroll positions while the Collie view shows.
A change between the layouts keeps the conversation and the draft text of the quick chat.

The quick chat holds these items, from top to bottom:

1. The header: the name "Collie", the model control, the actions menu, the close control of the wide layout, and a second row with the task number and the task title
2. The transcript, which scrolls inside the quick chat
3. The composer, which accepts text only

The actions menu holds "Open in Collie" and "New conversation".
"Open in Collie" selects the same conversation on the Collie page.
"New conversation" ends the current conversation of the packet and starts an empty conversation.
Both items are available only when the current conversation holds a message.

The model control shows the model and the harness of the current conversation.
The control opens the list of enabled agent profiles. A selection sets the Collie route for each new Collie conversation.
When the current conversation uses another model, a selection starts a new conversation of the packet with the selected model.
For a conversation with messages, an in-app confirmation dialog comes first. The previous conversation stays in the session list of the Collie page.
The model control is unavailable while a message goes to Collie, while Collie prepares a reply, and for a packet of an archived Project.

A packet without messages in its current conversation shows an empty state.
The empty state names the parts of the packet content that Collie receives.
For a task that the Strategist proposed, the empty state names the Strategic rationale in place of the thesis.
After a Revise decision, the empty state of the new packet links to the current conversation of the earlier packet.
The earlier packet is the packet of the same task directly before the new packet.
The link shows only when that conversation holds a message. The link selects that conversation on the Collie page.

For a packet of an archived Project, the quick chat states that the Project is archived.
The composer and "New conversation" are then unavailable. The history of the current conversation stays visible.

An open of the quick chat moves the keyboard focus to the composer.
When an arrow key, Home, or End selects "Collie" in the view row, the keyboard focus stays in the view row.
A reload with an open quick chat moves no keyboard focus.
The close control closes the chat column and moves the keyboard focus to the "Ask Collie" control.
The focus on the composer prepares the conversation, so the operator can type the first message while the Collie container starts.

Enter sends the message. Shift+Enter adds a line.
A sent message shows in the transcript at once, and the composer clears.
The quick chat shows partial reply text and reply states, as the Collie page does.
A send to an ended conversation resumes the conversation and keeps its history.
A failed preparation or a failed send states the cause and keeps the draft text.

These items stay the same when the quick chat opens or closes:

- The open packet
- The view
- The scroll position of the content area
- The selected file of the Files changed view
- The text of the reason field

The decision controls keep their availability while the quick chat is open and while Collie writes a reply.
The reader shows a decision that Collie records at the next data refresh of the page.

When the reader moves to another packet, the open quick chat shows the current conversation of that packet.
A packet without a current conversation shows the empty state.
A move to another packet creates no conversation and starts no container.
The quick chat sends each message to the current conversation of the open packet.
Unsent draft text stays with its packet until the page reloads.
When a newer packet of the same task replaces the open packet, the quick chat keeps the draft text and the failure statement.
A return to a packet shows its current conversation with the full history.

An open quick chat with an unanswered message refreshes each second.
Other states of an open quick chat refresh every ten seconds.
A closed quick chat reads no conversation.

### Report area

The report is the file that the Packager renders for a packet.
The report is an HTML file, a Markdown file, or a plain-text file.
The report area fills the space between the view row and the decision bar.
The report area uses the full width of the reader.

An HTML report fills the full report area in its sandboxed frame.
The report frame has a sandbox grant and a Content Security Policy.
No script runs in the report frame. The report frame loads nothing from the network.

The report frame follows these link rules:

- A click on an in-page link moves the report inside the frame. A link with an empty address is an in-page link.
- A click on a link to an absolute address opens a new browser tab.
- A click on a script link or on another link to a relative address has no effect. That link shows as plain text.

No dashboard frame navigates to another address.
Each dashboard frame that shows a report follows the same link rules.
These frames include the report frames of the Board task dialog.

Markdown reports and plain-text reports use the same report area and the same single scroll area.
Markdown reports and plain-text reports have a maximum line width that keeps the text easy to read.
Markdown reports use the dashboard Markdown renderer.
The page translates the container paths in these reports to host paths.

The report area shows the thesis only when the report area shows no report and the page is not reading the report.
The thesis is the opening text that Culler stores for the packet.
For a task that the Strategist proposed, the thesis has the heading "Strategic rationale" and uses the dashboard Markdown renderer.
That thesis holds the Packager's statement of the delivered change and then the Strategic rationale of the task.
For a task that the operator defined, the thesis has the heading "Thesis".
While the report loads, the report area shows a spinner, the status "Loading packet…", and placeholder lines in the shape of a report.
The report area shows no thesis or other packet text until the read ends.
The report area marks itself busy for assistive technology during the read.
For a packet without a recorded report, the report area states that no report exists yet.

Culler deletes the task artifacts and the change data when they expire. The spine keeps the records of the packet.
For an expired report, the report area states the expiry date.
The report area then shows the thesis and the criteria through the dashboard Markdown renderer.
The page shows no error notice for an expired report.

### Decision bar

The decision bar stays at the bottom of the reader.
In the wide layout, the decision bar starts with the "Ask Collie" control for each open packet.
The "Ask Collie" control uses the primary action colors of the selected theme.
It opens and closes the chat column.
The decision bar holds the Approve, Revise, and Cancel controls for a packet that awaits a decision and has no block.
The decision bar omits the approval scope text and the general explanation of reason requirements.

Approve uses no reason. Revise and Cancel require a reason.
Revise and Cancel open a reason field in the decision bar.
The reason field holds the Send control and the "Never mind" control.

A decision requires the report of the packet.
The decision controls become available only after the report of the packet loads.
For an absent report or an expired report, the decision bar states that a decision requires the report.
The decision controls then stay unavailable.

The decision identifies the exact packet and submitted version.
The command remains the authority on every decision.
While any decision request remains incomplete, the decision controls stay unavailable.
A Revise or Cancel request keeps the open packet and the reason field text.
The previous and next controls and the close control are then unavailable.
A click on a packet row, a view change, and a Ledger filter change then keep the open packet.
An Approve request closes the reader and leaves the packet list available.

A click on Approve closes the packet immediately, before the approval request completes.
The reader place names the task and states that the page sends its approval.
The sidebar shows an animated indicator and "Approval in progress" until the request completes.
The packet remains undecided until the command confirms approval.
A failed approval restores the row without a progress indicator and shows an error that names the task.
The request retains the exact packet and version that the operator reviewed.
On a wide screen, the reader opens the next packet that awaits a decision, if one exists.
On a narrow screen, the page shows the packet list.
The approved packet stays in the Live queue and shows its merge state.
An animated indicator and "Merging..." distinguish each approved packet until its merge completes.
The indicator remains visible with reduced motion, but its animation stops.
The page updates the approval state and decision count before it requests fresh packet data.
A failed data request preserves the approval and states that the decision was saved.
After a successful Revise or Cancel decision, the reader keeps the decided packet and shows its decision text.
When the reader shows the decided packet, a failed decision keeps the reason field and its text.
When the reader shows another packet or no packet, the error of a failed decision names the task.

For each other review state, the decision bar shows the decision text without decision controls:

- An Approved packet shows the approval time and the merge state text.
- A Merged packet shows the approval time, the destination branch, and the merge commit.
- A Cancelled packet shows the cancel time and the reason.
- A packet in the Returned to the pipeline state shows the return and its cause.

Each time in the decision text shows the local date and time.

For a blocked task, the decision bar states the block reason.
The decision bar of a blocked task shows no decision controls.
For a stalled task without a block, a packet that awaits a decision shows no decision controls.
In the narrow layout, the reader shows the decision bar only when the bar has text or decision controls.

### Escape key

The Escape key acts on the first case of this list that applies:

1. The reason field is open and empty. Escape closes the reason field.
2. The reason field holds text. Escape keeps the reason field and its text.
3. In the narrow layout, Escape closes the reader as the close control does.

An open task dialog takes Escape first. Escape then closes the task dialog and keeps the reader open.
The Review page acts on Escape only while the keyboard focus is on the Review page or on no control.
Escape has no effect while the keyboard focus is inside the report frame.
Escape has no effect while the page sends a decision.

## Pipeline

The Pipeline page shows the selected Project's role map and task counts.
The task path shows these roles:

- Strategist
- Arbiter
- Worker
- Verifier
- Packager

The Packager follows source verification.
The Scribe appears in a separate Project wiki section.
The Scribe card shows its state and its last run or active run.
The wiki section shows the recorded wiki state or the failure to read that state.
The Strategist proposes two tasks, and the Arbiter selects from the two proposals.
The role cards adapt to the available width without horizontal scroll.

A banner shows the current task run with these facts:

- The role and run state
- The task title and identifier, when the run has a task
- The Project
- The run age, or the launch wait before the agent starts
- The last heartbeat, or the absence of a heartbeat
- The trace link, when the run has a main trace

An idle Project shows **No active task run**.
A paused Project also shows **Project paused**.
A Scribe run stays in the wiki section and produces no task run banner.
The Project wiki section is to the right of the banner, above the task path.
In a narrow viewport, the Project wiki section follows the banner.
The Activity section follows the task path directly.
Operator decisions and diagnostics appear only in the persistent notice area.

The task path shows current run states and task counts during a Verifier correction.
The recent runs retain the correction request as a historical result.

Each task role card shows these facts on separate rows:

- The role name
- The role purpose
- The state of the role
- The Project and the age of the active run, or the age of the last run
- The task count at the stage after the role

The state of a pipeline role is one of these values:

- Idle
- Agent launch pending
- Running now
- Heartbeat stopped

Each text stays inside its card and clear of each other text.
A long Project name wraps inside the card.
A card distinguishes an active run, a launch wait, and a stopped heartbeat.

The selected Project supplies these facts:

- The active lease and its heartbeat
- Each role's recent runs
- The task count at each stage
- The recent pipeline runs, their diagnostic links, and their trace links
- The wiki state of the Project and its last wiki update

The role map and the recent runs show only agent runs. A repository operation appears in neither.
Each recent run states its result in plain words.
A wiki update run appears as a Scribe run with no task. Its row states the result of the wiki update and the cause of a failed wiki update.
The row has the diagnostic link and the trace link of the run.

A role selection opens its details in a centered modal dialog.
The dialog fits the viewport and preserves the positions of the page components.
The Agents section of Configs uses the same dialog behavior.
The dialog keeps keyboard focus until it closes.
The focus then returns to the selected role.
The role map marks the selected role.
The role detail shows these facts:

- The role name
- The state of the role
- The role description

The role detail of the Scribe holds the same parts as the role detail of each task role.

The role detail holds one row for each configuration:

- **Agent profile** shows the model of the effective shared route. The row states that it applies to all Projects.
- **Custom instructions** shows the current revision, or that the role has no custom instructions. The row states that it applies to the selected Project.

The **Custom instructions** row shows a customization conflict that blocks the role.

Each row opens one dialog.
The role detail stays open behind the dialog.
The focus returns to the row when the dialog closes.
Each of these actions closes the role detail:

- **Close**
- Escape
- A press outside the role detail

The **Custom instructions** row opens the Project role customization screen for its role.
Project configuration opens the same screen with a role selector.
The dialog title identifies the role and invites the operator to guide its work.
The dialog shows the Project title.
A concise description explains the purpose of the instructions for the selected role.
The editor has a visible label and states when the instructions take effect.
The primary action saves the instructions.

A short description explains the core responsibilities of each role in plain language.
The responsibilities and revision history appear directly in the dialog.
The revision history includes controls that load prior text into the editor.

Saved instructions apply to new runs.
An empty save leaves the standard directive in effect.
A failed save preserves the proposed text.
A role block notice directs the operator to Collie for resolution.
A stale save requires a current revision read before another save.
A save preserves each block and the Project pause state.

### Activity

The Activity section shows events and dispatch health errors for the selected Project.
The command associates records through these recorded identities:

- Task
- Run
- Project
- Connection
- Dispatch result

The command applies the Project and kind filters before the row limit.
Unassigned system events and Collie events remain outside the Pipeline page.
System Logs provides the global process logs.

The Activity section contains one component with Events and Dispatch errors tabs.
The section explains that dispatch errors come from checks before a run starts.
The explanation directs the operator to Recent runs for run failures.
An empty Dispatch errors tab describes only the selected dispatch records.

Events is the default tab.
Each tab shows its record count.
The selected tab shows its stream.
The tabs support keyboard selection.
The selected tab persists across Project and page changes.

Each record shows these facts:

- Actor
- Subject
- Detail
- Time

The subject states its kind: run, task, connection, or Project.
A spawn record exists only for a run that dispatch claimed. The record names the role, the run, and the task of a run that has one.
A wiki update has one record at its start and one record at its end.
The end record states the result and the cause of a failed wiki update.
When a connection check stops a spawn, Dispatch errors shows one record when the wait starts and one record when the wait ends.
Both records name the connection.

The live tail merges records by identity and bounds its retained rows.
A paused tail preserves its displayed records and acknowledges no additional records.
A Project or filter change clears both streams, including when the tail is paused.
The page acknowledges health records only when the Dispatch errors tab displays them and the live tail is active.
An error for another Project remains undelivered.

The Activity section follows the role map and precedes the recent runs.
A failed activity read clears the streams and shows the error.
The role map and recent runs refresh independently of the activity read.
Saved Activity links open Pipeline.

## Configs

Configs contains global configuration and deployment facts:

- Editable configuration values and their default or operator source
- Shared role routes and enabled runtime choices
- The Project registry and sandbox profiles
- Outbox delivery lag
- Host health from periodic reads
- Deployment checks from periodic inspections

The Configs sidebar contains these sections in this order:

- **System**: deployment health, trace access, and delivery status.
- **Agents**: shared role assignments and the Model configuration control.
- **Projects**: the registry, environment actions, role customization, and pipeline pause controls.
- **Discord**: Collie conversations and alerts through one configured channel.
- **Advanced settings**: supported configuration editors.

The Discord link appears after the first Project completes initial source acceptance.
The availability survives a Project deletion and a Discord disconnect.
A configured Discord connection also permits access to the section.
The section appears without a prompt or automatic navigation.

Each sidebar link has a decorative outline icon to the left of its label:

- System uses two server units.
- Agents uses a robot head.
- Projects uses a folder.
- Discord uses two message bubbles.
- Advanced settings uses horizontal adjustment controls.

The icons inherit the text color and the selected state.
The section labels remain visible on narrow displays.

A bare Configs address opens System.
Each section has its own address. Reload and browser history restore the section.
The Configs tab retains the selected section for the current page session.

The sidebar shows unsaved changes beside Advanced settings.
Section changes preserve drafts, filters, and open details.
Narrow displays show the available section links above the content.
Both themes retain the existing Culler colors, typefaces, and controls.
System notices remain visible outside the section content.

AgentsView access requires only the trace link while the operator has valid dashboard access.
The Configs page omits the entire **Traces** section and its space in the layout.
AgentsView token instructions and the **Copy AgentsView token** control remain absent from Configs.
Access failures appear beside the affected link or on the Culler error page.

The Advanced settings section shows its supported editors and key filter after selection.
Each editor states when its saved value takes effect.
The configuration response contains only settings that the dashboard can edit and apply.
The wiki update interval is an editable setting.
The wiki update interval editor shows and accepts minutes.
The setup procedure owns deployment values and Git author defaults.
Model configuration owns connections and agent profiles.

A save preserves unrelated values and rejects stale edits.
The dashboard retains unsaved edits across periodic reads and failed saves.
A successful save confirms file persistence and any required spine update.
An incomplete spine update reports the saved state and permits a retry.
**Delivery status** starts closed and retains the delivery counts.

A role route change applies to all Projects.
A rejected route change preserves the confirmed route and shows the command error.
Each Project row provides its pipeline pause control.

### Discord

The initial card explains the connection and provides **Set up Discord**.
One bot and channel serve the Culler installation.

The setup flow contains three steps:

1. **Connect bot** validates a masked token and shows the bot identity.
2. **Choose channel** lists server and channel names and sends an explicit test message.
3. **Confirm account** receives a temporary code from the operator's Discord account.

The code expires after five minutes.
The code belongs to the selected bot and destination.
Replies from bots or other destinations grant no access.
The operator confirms the displayed account before activation.
The flow preserves an active connection until a verified replacement is ready.

The connection card shows the destination and authorized accounts.
Its status reflects the active connector.
The card provides separate controls for blocked task alerts and system health alerts.
Those controls preserve dashboard alerts.

The card provides these actions:

- **Send test message** reports confirmed delivery.
- Each channel message starts a conversation in its own Discord thread.
- Thread replies and dashboard replies continue the same conversation.
- The dashboard shows conversations from both surfaces.
- Conversations that start in the dashboard remain outside Discord.
- Create Public Threads is a required bot permission.
- **Check connection** checks bot access and connector health.
- **Edit connection** reuses the setup flow and can retain the stored token.
- **Disconnect** requests confirmation before it removes the connection and token.

The host applies changes without an install command or manual process restart.
The resident owns the connection and temporary setup state.
The `mc` command owns the saved configuration and alert routes.
The dashboard exposes neither the stored token nor private activation commands.
Agent containers hold no authority to configure the connection.

A destination change retires undelivered messages for the old connection.
Those messages retain their original destination and receive no delivery receipt.
Disconnect preserves Project data and conversation history.
An error identifies the affected operation and provides a retry.

### Project deletion

Each Project row provides **Delete Project** at its right edge.
The confirmation names the Project and lists the task, review, and setup operation counts.
The confirmation states that deletion is permanent.
It states that the source repository and other Projects remain intact.
Cancel closes the confirmation without a change.

Deletion stops the Project containers immediately, including its setup conversations.
Deletion permits active, paused, and archived Projects.
Culler prevents new Project container launches during deletion.
Culler verifies container removal before it deletes the database records.
A container cleanup failure preserves the database records and permits another deletion attempt.
A failed attempt can leave some containers stopped.

A failed read keeps deletion unavailable and offers **Check again**.
The confirmation shows progress and blocks duplicate submissions while deletion runs.
A rejected deletion preserves the Project and shows the command error.

The command requires host scope and explicit confirmation.
The dashboard requires its token and a local origin for the request.
The command deletes all selected records in one database transaction after container cleanup.
A database error rolls back the transaction.
Database record protections permit deletion only for the selected records during this transaction.

Deletion removes the Project and its dependent records, including every Project setup record.
The Project setup records include these records:

- Vision drafts and submissions.
- Environment revisions, approvals, operations, reports, and image decisions.
- Environment evidence, cache metadata, runtime checks, and source associations.
- Project setup conversations and their message, attachment, and delivery records.

Shared profiles, image sources, credentials, and configuration remain intact.
Deletion preserves source repositories and their files.
Culler retains diagnostic files on disk.
The dashboard refreshes the Project registry and selects another Project when the selected Project disappears.
Deletion of the last Project clears the selection and shows the empty Projects page.
Task and review identifiers remain unique after deletion.

## System Logs

System Logs provides global resident, dashboard, AgentsView, and command logs.
The page also provides the recorded run diagnostics.
Unit and stream choices preserve their address-bar links.
The page supports a row limit, older records, text search, line wrap, and copy.
Structured records support a severity filter.
Plain log text remains opaque.
AgentsView writes plain text to three streams: stderr, stdout, and debug.
The AgentsView entry opens its debug stream. The address bar selects the other streams.
A missing log shows an explicit empty state.

## Pipeline pause controls

The Project list contains the pipeline pause controls.
The list footer contains one bulk control when at least two Projects qualify.
A Project qualifies when its setup is complete and its state is not archived.
Each Project row supplies its status. The footer repeats no status.
The list displays a failed pause request beside its controls, including when the bulk control is hidden.
The control shows Resume all when every Project with complete setup is paused.
The control shows Pause all in the other states.
Each list entry contains the control of its Project: Pause, or Resume when the Project is paused.
Archived Projects have no pause action.
The Project button shows a pause mark when the selected Project is paused.
The lease badge states the pause of the selected Project.
The controls show the confirmed pause state.

A pause blocks new pipeline dispatch for its Project.
Pause all pauses every active Project.
Active runs continue. The resident processes run completion and container failure.
A dispatch committed before a pause can still start afterward.

Resume all resumes every paused Project with complete setup.
Projects with incomplete Vision setup and configured Projects retain their pause.
The database preserves pause state across process restarts.

## Verification and release

Each changed page requires a real browser test against a scratch database.
Tests must cover multiple Projects and exclude records from the unselected Project.
Tests must cover empty states, failed reads, and delayed responses.
The verify-behavior skill supplies the procedure for visual evidence.

The dashboard server remains a thin command interface.
Browser modules require entries in the server registry and the host release manifest.
The dashboard fast checks and browser tests must pass before release.

### Agent profiles

Pipeline and Configs expose **Model configuration** through the same side panel.
The panel states that model configuration applies to all Projects.
The Pipeline role detail exposes the **Agent profile** row.
The Configs role detail exposes **Change agent profile**.
Each control opens the assignment view for its role.

The assignment view is a dialog in the middle of the page.
Its heading states the role. It states that the assignment applies to all Projects.
It lists each enabled agent profile as one choice.
Each choice shows the model identifier, the agent profile name, and the connection access.
A selection assigns the agent profile at once.
Each choice offers **Edit** for its agent profile.
**Edit** preserves the role assignment.
**Back** and **Save** return to the same assignment view.
The assignment view offers **Create agent profile…**.
The Configs selector offers enabled agent profiles and **Create agent profile…**.

The panel lists only enabled agent profiles.
The profile count counts only the listed agent profiles.
The panel shows compact rows with the model identifier as the main label.
Each row shows its connection access and role use.
Each row offers **Edit**.
**Details** reveals the connection, its current access, and the effort.
Each profile row and assignment choice shows its effort.

The **Connections** disclosure lists each saved connection and its **Disconnect** control.
The confirmation names the connection and states that active requests stop.
Disconnection removes the saved credential and preserves agent profiles and assignments.
The result distinguishes local disconnection from confirmed provider revocation.
A stale confirmation preserves a replacement credential and requires current confirmation.

The rows and the details omit the enabled state.
Each row status shows the current connection state.
Agent profiles with a current connection failure or model failure appear first.
These failures receive attention colors.
An account connection without usable access offers **Authorize** in the panel.
An application programming interface (API) key connection with a rejected key offers **Replace API key**.
A host credential failure names the Keychain recovery action.
A host proxy failure names the host recovery action.
A quota failure names the provider allowance action.
The panel shows a provider name only from the release catalog or from a connection name.

The panel header and **Close** remain visible when the operator scrolls.
Each panel view shows **Close** and a maximum of one other navigation control.
The assignment and repair views show **All agent profiles**.
**All agent profiles** opens the list of enabled agent profiles.

The creation form shows **Back**.
**Back** opens the previous screen of the form.
From the first screen, **Back** opens the view that opened the form.

The operator can create a profile with an existing connection through one form.
**Use an existing connection** lists only connections with usable access.
A supported connection requires a saved credential and no current connection failure for reuse.
When no connection has usable access, the form directs the operator to **Back** and the connection choices.
The Edit form keeps the current connection of its agent profile in the list.

The first screen of the creation form offers one choice for each provider with a subscription integration, then **Use another provider**, then **Use an existing connection**.
The release catalog supplies the provider names, and the configuration template supplies the subscription integrations on every deployment.
A provider with a subscription integration offers its sign-in first and its API key second.
**Use another provider** offers the provider search of the release catalog and one API key field.
The search lists the providers that OpenCode runs, and an empty search lists each of them.
The selected provider has a selection mark, a checked selector, and a summary above the key field.
The summary remains when a new search hides the selected provider.
The form names each new connection after its provider and its access method, and it requests no connection name.
A connection with that name and no credential receives the new credential, unless an enabled agent profile uses it.
Another connection with that name gives the new connection the next free number.
The API key form contains one concealed key field.
A successful save clears that field.
The browser keeps the key out of browser storage and diagnostic records.
A failed model test preserves the saved connection and the nonsecret form values.
A retry uses the saved connection without another key entry.

A new connection uses three steps:

1. Connect.
2. Configure.
3. Test and save.

A step indicator shows the completed steps, the current step, and the next steps.
The step indicator accepts no selection.
Each step shows **Back** and its next action in one row below the form.
The form preserves nonsecret values during corrections.

The account form offers Codex sign-in with an OpenAI account and Claude Code sign-in with an Anthropic account.
The form suggests only the agent profile name.
Each suggestion follows its source until the operator enters another value.
A change of the connection type clears each suggestion.

A connected account or API key connection offers its models in a selector.
The provider supplies this list for the connection.
For a Codex subscription, the list contains the models that the Codex version of the runner image supports.
The selector keeps a configured model that the list omits.
An alert states that the connection does not offer that model.
A failed list shows its error, a model identifier field, and **Retry model list**.
A Codex model test stops before the provider request when the list omits the model.

The profile form places **Effort** beside **Model**.
**Default** leaves the harness effort unset.
The selector uses the effort levels that the model lists and the installed harness accepts.
When the model lists no effort capabilities, the selector identifies its choices as harness levels.
A model test verifies that selection before the profile save.
A change of the model or connection resets the effort to **Default**.
The review shows the selected effort.
A change of effort invalidates the previous model test.
The backend validates and saves the effort with the agent profile.
Each new run receives the selected effort through its immutable launch envelope.
A Collie conversation retains its original effort across restarts and profile changes.
Current connection and model access checks still control each Collie launch and reply.

Each model test requires an explicit action.
The review offers two actions: **Test model** and **Save**.
**Save** requires a successful model test of the current values.
Each role selector offers a saved profile.
The review holds no assignment action.
The review shows the model test state and each review error above its actions.
A model test in progress shows a progress indicator, the elapsed time, and **Cancel test**.
A finished model test shows its result and the next action.
**Check status** appears only when the operation status is unavailable.

Creation from a role selector returns to that role selector after the save.
The operator selects the new profile in that selector to assign it.
An assignment failure keeps the current assignment and permits another selection.
Active runs retain their original configuration.
The panel supports keyboard access and fills narrow displays.
The panel restores focus to its entry control when it closes.
