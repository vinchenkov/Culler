# Dashboard conventions

## Purpose

The dashboard must use the Culler blue theme across its shared components.
Equivalent components must share their appearance and interaction rules.
Each page must retain the structure that its content requires.

The [theme viewer](index.html) provides the visual reference under **A · Culler blue**.
This specification defines the required appearance, behavior, and accessibility.
The dashboard supplies the actual content and controls.

## Scope

The conventions must cover these surfaces:

- The application navigation and Project selector.
- Board and its Setup Journal and task dialogs.
- Review and its packet reader and panels.
- Pipeline and its role details and agent profile assignment.
- Collie and its sessions and conversations.
- Configs and its system details and Project controls.
- System Logs and its controls.
- Project Vision and Environment.
- Each Culler dialog, side panel, menu, and notice within these surfaces.
- Culler pages that provide trace access or explain a trace access failure.

The conventions must apply in both light and dark themes.
The theme control must retain its current selection and persistence behavior.
The first paint must use the selected theme.

The application must retain its navy navigation band, logo, and cream navigation text.
Each theme must retain its current background image.
The background veil must preserve the contrast of page content.

## Applicability

A structural header identifies a bounded dialog or panel.
A content title identifies a section within a page or panel.

Structural headers must use the shared blue header appearance.
Content titles must use the text appearance of their surface.
Page titles must remain on the page background.
Simple cards must retain their compact layout when they have no structural header.

| Surface | Required convention |
| --- | --- |
| Dialogs and side panels | The shared blue header, body surface, boundary, and action styles apply. |
| Named page panels | The shared blue header applies to a structural header. |
| Page titles and descriptions | The shared text hierarchy applies on the page background. |
| Cards, rows, tables, and content titles | The shared surfaces and controls apply within their current content structure. |
| Board columns and Pipeline stages | The stage colors retain their current associations. |
| Menus and small contextual panels | The shared surface, boundary, selection, and focus styles apply. |
| Notices and status labels | The shared status styles identify the actual condition. |
| Specialized content | The exceptions below define the content appearance. |

The convention must preserve these specialized surfaces:

- A report frame must retain its light sheet and the report's own colors.
- The log content region must retain its dark appearance in both themes.
- Source code and file comparisons must retain their syntax and change colors.
- The Setup Journal approval view must retain its navy surface and its required contrast.
- OpenWiki and AgentsView must retain their own appearance.
- An agent artifact must retain its own content and colors.

Each Culler control around specialized content must use the shared conventions.
Culler trace access pages must use the shared colors and control styles.
Those pages must retain their access controls and security boundaries.

## Theme colors

Each color role must have one shared definition per theme.
Components must obtain their colors from these roles.
Each component must use the same role for the same purpose.

| Color role | Light theme | Dark theme |
| --- | --- | --- |
| Page base | `#f2f3f4` | `#101d2a` |
| Body surface | `#fdfdfe` | `#192c3e` |
| Subtle surface | `#eef1f4` | `#203548` |
| Main text | `#172f47` | `#edf3fa` |
| Secondary text | `#485b6f` | `#b6c6d7` |
| Neutral boundary | `#dbe1e6` | `#3b5065` |
| Control boundary | `#6f879f` | `#71889f` |
| Structural header | `#1f5078` | `#234e73` |
| Header title | `#ffffff` | `#ffffff` |
| Header context | `#e1edf7` | `#d6e8f7` |
| Primary action | `#1f5078` | `#94c7f0` |
| Primary action text | `#ffffff` | `#102f49` |
| Selected surface | `#e6edf4` | `#25435e` |
| Selected boundary | `#1f5078` | `#8dc3ef` |

The page base must remain visible beneath the background image and its veil.
The body surface must supply the standard dialog and panel background.
The subtle surface must identify secondary content and action footers.

These secondary regions must use the subtle surface when applicable:

- A group of facts.
- A reference to role responsibilities.
- Explanatory content within a dialog.
- A secondary toolbar or action footer.

Field surfaces must use the body surface.
Text on each surface must use its appropriate text role.
Hover, focus, disabled, and status styles must use shared color roles.
Contextual field boundaries can use additional lines to meet the contrast requirements.

Ordinary page components must use the shared theme definitions exclusively.
A specialized surface can define the colors that its content requires.

## Shape and text

Equivalent components must share these dimensions:

| Component property | Dimension |
| --- | --- |
| Dialog and bounded panel corner radius | 16 pixels (px) |
| Field, nested content, and selected row corner radius | 9 px |
| Button corner radius | 7 px |
| Dialog horizontal inset | 24 px |
| Compact panel horizontal inset | 16 px |
| Close button size with a precise pointer | 36 px square |
| Minimum action target with a coarse pointer | 44 px square |

Headers, bodies, and footers must align their content along the same horizontal inset.
Narrow layouts can reduce the inset consistently across all three regions.
Detached dialogs must share one soft shadow per theme.
Embedded panels must use a restrained boundary and shadow.
Nested content must use boundaries and surfaces that preserve the content hierarchy.

The logo must retain Fraunces.
Interface text must use Figtree.
Code and literal identifiers must use the shared monospace font.

Text styles must distinguish these roles:

- Page titles.
- Dialog titles.
- Panel titles.
- Content titles.
- Body text and field labels.
- Secondary text and metadata.

Each role must use one shared size and weight within the same density.
Dialog titles must use sentence case.
Panel titles and action labels must use sentence case, except established Project names and role names.
Body text must preserve a readable line length.
Dialog titles and field labels must wrap without overlap with controls.
Compact titles can retain their current truncation and access to the full text.

## Dialogs

Each dialog must contain a shared header and body.
The header must contain the title and a close button.
The header can contain concise context and status labels.

The close button must occupy the upper right corner.
The close button must show the shared cross and provide the accessible name **Close**.
Closure must retain each dialog's draft policy.
A tooltip must describe special closure behavior when applicable.

The header must use the structural header color in every dialog state.
The body must use the body surface in every dialog state.
An error must appear within the body, near the affected control or result.
A dangerous action must retain the blue header and use the danger action style.

Dialog forms and confirmations must place their final actions in a shared footer.
The footer must use the subtle surface and a neutral top boundary.
The primary action must appear after the secondary actions.
The primary action must align to the right when the layout permits.
An immediate action must retain its current behavior and location.
A dialog without a final action can omit the footer.

Each dialog must retain the width that its content requires.
Task details can retain multiple columns on wide screens.
An agent profile assignment can retain a compact width.
Model configuration must retain its side panel layout.

The header and any action footer must remain available while the body scrolls.
The body must use one vertical scroll region when the dialog exceeds the viewport height.
Long content must remain accessible at short viewport heights.
The dialog must fit within the viewport width.

Every top-level modal dialog must use one shared backdrop per theme.
The backdrop must cover the application navigation and page content.
Stacked dialogs must use the shared lighter backdrop above the lower dialog.
The combined backdrop must preserve the visible context of the lower dialog.

## Panels and page content

Named panels must use the dialog header colors for their structural headers.
Collie's Sessions and Conversation panels must use those colors.
The same rule must cover the structural headers of Vision and Environment panels.
The panel body must use the body surface.

A panel header can retain its title and contextual controls.
A header control must have a readable surface and text color in both themes.
Header controls must use shared styles for each interaction state.
A model selector within a blue header must retain a distinct field surface.

The Project selector and contextual menus must retain their compact structure.
Selected entries must use the selected surface and an accessible selected state.
Menus must retain their current keyboard behavior and dismissal rules.

The Review reader must retain its wide and narrow layouts.
Its structural headers and side panel headers must use the shared conventions.
Its document titles, view controls, and decision controls must retain their established functions.

The Setup Journal must retain its milestone hierarchy and progress display.
Its structural header must use the shared conventions.
Milestone titles must remain content titles.
The approval view must retain its specialized surface.

## Controls and status

Buttons must share their size, shape, text, and interaction styles within each density.
Primary actions must use the primary action colors.
Each action group must emphasize at most one primary action.
Secondary actions must use the body surface and the control boundary.
Header actions must use shared colors that contrast with the blue header.

Dangerous actions must use red text and an explicit action label.
Neutral actions must retain a neutral appearance.

Every control must define these states when applicable:

- Rest.
- Hover.
- Focus.
- Pressed or selected.
- Disabled.
- An operation in progress.
- An error.

Disabled controls must remain identifiable and legible.
An operation in progress must retain its actual progress and submission protections.
The interface must preserve each current success, failure, and retry state.

Form fields must share their surface, boundary, radius, and focus appearance.
Labels must appear above fields, except compact selectors and native choice groups.
Help text must appear near its field.
A placeholder must supplement a label rather than replace it.

Status styles must preserve these meanings:

- Blue identifies active work or selection.
- Green identifies success or completion.
- Orange or amber identifies a condition that requires operator attention.
- Red identifies failure or a dangerous action.
- Neutral colors identify an idle, paused, or unavailable state when no failure applies.

Each status must include text or an accessible label.
Structural header colors must remain independent of operational status.
Board stage colors must retain their current stage associations.
Unknown or unavailable data must retain its truthful state.

## Accessibility and responsive behavior

Culler interface text must have a contrast ratio of at least 4.5:1 against its actual background.
Control boundaries and focus indicators must have a contrast ratio of at least 3:1 against adjacent surfaces.
Input boundaries must remain distinct from both the field surface and the adjacent surface.
Contrast evidence must include controls within blue headers.

Every modal dialog must retain native modal behavior and an accessible title.
Keyboard focus must remain within the active modal dialog.
Escape must retain each dialog's current cancellation protections.
Closure must restore focus to the opener or its appropriate replacement.
A refresh must preserve focus, selection, and unsaved form values.

The interface must retain access to all content and controls at these viewport sizes:

- 1440 × 900 px.
- 1000 × 700 px.
- 390 × 844 px.
- 390 × 667 px.
- 320 × 667 px.

At 200 percent browser zoom, the interface must preserve readable content and access to controls.
Actions must wrap or stack when their horizontal space is insufficient.
Titles and controls must remain separate.
The page must preserve its current responsive navigation and panel behavior.

Ordinary content must fit without horizontal page scroll.
Specialized content must retain the scroll rules in its applicable specification.
Reduced motion preferences must remain effective.

## Behavior and architecture

The dashboard must retain its page set and navigation destinations.
Each current control must retain its domain action and authorization requirements.
Each current form must retain its validation and submission rules.
Each current data view must retain its records and Project scope.

The actual dashboard behavior must govern these cases:

- Agent profile selection must retain its immediate assignment behavior.
- Project deletion must retain its current checks, disclosures, and confirmation rules.
- Task actions must retain their availability rules and current effects.
- Review decisions must retain their current confirmation and evidence requirements.
- Collie must retain its session, model, attachment, and transcript behavior.
- Environment actions must retain their setup and image approval rules.

Product actions, confirmation fields, and submission requirements must follow the actual dashboard.
The interface must retain the current progress, error, and recovery behavior during asynchronous operations.

Shared components must own the repeated appearance and structural behavior.
Pages must supply their content, state, and domain actions.
Equivalent controls must obtain their appearance from the same shared definition.

The shared definitions must cover these component families:

- Dialog headers, bodies, footers, and close controls.
- Panel headers, bodies, and action regions.
- Buttons and contextual actions.
- Form fields, labels, help text, and errors.
- Status labels and notices.

The browser code must retain the dependency-free application architecture.
Text from agents or repositories must retain safe text and Markdown display.
The dashboard must retain its command interface as the sole path to Culler state.
Component extraction must preserve the smoke tests' `data-testid` hooks.
Each new browser module must appear in the server registry and host release manifest.
The application entry point must import each new browser module that it requires.

## Acceptance evidence

The implementation must include an inventory of the affected components and their shared definitions.
The inventory must identify each specialized surface and its reason.
The inventory must account for every surface in the scope above.
The dashboard page requirements in `dashboard/PAGES.md` must describe the implemented conventions and retained exceptions.

Browser evidence must use the repository's dashboard smoke harness against a scratch database.
The evidence must show the actual dashboard rather than the mockup.
The evidence must include both themes and the viewport sizes above.

The evidence must cover these dialogs and side panels:

- Add Project and Delete Project.
- Role instructions and their revision history.
- Task details and each Board action form.
- Agent profile assignment and Model configuration.
- Agent profile creation, edit, repair, and model test results.
- System issue details and technical details.
- Session rename, session deletion, and model change confirmation.
- Culler trace access and failure pages.
- Evaluation cases and their score controls.

The evidence must cover these page states:

- Each main page and both dedicated Project pages.
- Collie sessions, the model selector, the transcript, and the composer.
- The Review queue, packet reader, decision controls, and side panels.
- Pipeline role details, active work, and recent activity.
- The Setup Journal, its approval view, and historical files.
- Empty content, delayed reads, failed reads, and current content after a refresh.
- Multiple Projects and the correct records after a Project change.

Automated browser checks must prove these properties:

- Equivalent headers use the same theme colors.
- Dialog and panel bodies use the shared surface colors.
- Specialized content preserves its required colors.
- Actual text, boundaries, and focus indicators meet the contrast requirements.
- Long content preserves access to the header, body, and actions.
- Stacked dialogs preserve the correct backdrop and focus behavior.
- Theme changes apply to open dialogs and panels.
- Theme changes preserve form values and the current view.
- Keyboard controls preserve their current actions and focus destinations.
- Responsive layouts preserve content and action access.
- The domain behaviors above remain effective.

Screenshots must show representative complete components in both themes.
The required dashboard checks and the complete browser smoke suite must pass.
The deployment checks must verify each new browser module and asset.
The implementation must retain the current release and browser evidence requirements.
