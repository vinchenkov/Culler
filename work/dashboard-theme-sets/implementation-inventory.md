# Dashboard convention inventory

## Shared definitions

The dashboard uses the Culler blue theme in both themes.
The theme roles in `dashboard/src/ui/style.css` control ordinary interface colors.
The same roles control these components:

- Fields.
- Buttons.
- Notices.
- Status labels.
- Selected rows.
The theme module preserves the selection and follows the system theme without a stored selection.

The dialog components in `dashboard/src/ui/components.js` supply the repeated structure:

- `dialogHeader` supplies the title and its close control. It also supplies context.
- `dialogBody` supplies the body region.
- `dialogFooter` supplies the final action region.
- `dialogFrame` assembles the regions within the dialog.
- `showDialog` records the native modal stack and opens the dialog.

The shared selectors define the repeated appearance:

- `.ui-dialog-head` supplies the blue structural header.
- `.ui-dialog-body` supplies the body surface and its vertical scroll region.
- `.ui-dialog-foot` supplies the subtle action footer.
- `.ui-dialog-close` supplies the cross and close control.
- `.btn` supplies the shared action appearance.
- `.field` and native form controls supply the shared field appearance.
- `.tag` and the notice classes supply status text and semantic colors.

The shared blue panel rules cover structural headers within each page structure.
Content titles retain the text color of their surface.
The stage definitions preserve Board and Pipeline associations.

## Surface inventory

| Surface | Shared conventions | Retained structure and behavior |
| --- | --- | --- |
| Application navigation | Theme text, controls, focus, and selected states | Navy band, logo, viewer links, responsive navigation, and Project scope |
| Project selector | Body surface, boundary, selected surface, and focus | Compact menu, keyboard selection, wiki links, and dismissal |
| Board | Cards, controls, status labels, and page text | Stage colors, task scope, card actions, and live refresh |
| Task details | Dialog header, body, cross, and common surfaces | Wide content columns, reports, facts, history, and task actions |
| Board action forms | Dialog regions, fields, errors, and final actions | Current checks, draft policy, confirmations, and submission protections |
| Setup Journal | Panel header, text, controls, and secondary regions | Milestones, progress, reports, files, and image decisions |
| Review queue | Surfaces, text, controls, status, and selection | Queue scope, packet selection, and reader entry |
| Packet reader | Structural headers, controls, and side panel surfaces | Wide and narrow layouts, documents, decisions, and evidence |
| Review side panels | Blue structural headers, bodies, controls, and close actions | Homie, run history, records, and retained conversation behavior |
| Pipeline | Common text, controls, surfaces, and status | Active work, role details, stage associations, and recent activity |
| Agent profile assignment | Shared dialog header, body, cross, fields, and selection | Compact layout and immediate assignment |
| Model configuration | Shared header, body, cross, fields, and actions | Side panel layout, profile checks, and access state |
| Agent profile forms | Shared header, body, cross, fields, notices, and actions | Create, edit, repair, model tests, and domain checks |
| Homie | Blue panel headers, body surface, fields, controls, and status | Sessions, transcripts, attachments, model selection, and composer |
| Session actions | Shared dialog regions, fields, danger controls, and actions | Rename, delete, model change, cancellation, and confirmation |
| Configs | Shared surfaces, fields, controls, status, and page text | Sidebar sections, drafts, Project controls, and authorization |
| Add Project | Shared dialog regions, fields, cross, notices, and actions | Folder checks, source access, retained draft, and import |
| Delete Project | Shared dialog regions, cross, danger action, and notices | Current disclosures, deletion checks, confirmation, and progress |
| Role instructions | Shared dialog regions, cross, fields, and subtle reference content | Role choice, instructions, revision history, stale drafts, and refresh |
| System issues | Shared dialog header, body, cross, notices, and controls | Current causes, recovery actions, and truthful unavailable states |
| Technical details | Shared dialog regions and literal text styles | Exact diagnostics, current status, and repair context |
| System Logs | Common page text, toolbar, fields, and controls | Dark log region, filters, records, and live refresh |
| Project Vision | Blue structural panel headers, surfaces, fields, and actions | Draft, revision, preview, save, and initialization |
| Project Environment | Blue structural panel headers, surfaces, fields, and actions | Setup operations, image choices, reports, and cache controls |
| Evaluation cases | Shared dialog regions, fields, notices, and score controls | Exact case identity, rubric, score drafts, and submission checks |
| Contextual menus | Shared body surface, boundary, selection, focus, and actions | Existing keyboard controls and dismissal |
| Notices | Shared semantic text, surface, boundary, and controls | Current success, attention, failure, progress, and retry states |
| Trace access pages | Culler blue colors, structural header, body, subtle footer, and primary action | Nonce protection, authorization, one-use grants, and separate browser origin |

## Specialized surfaces

| Surface | Exception | Reason |
| --- | --- | --- |
| Report frames | Light report sheet and report colors in both themes | The report supplies its own document appearance. |
| System Logs content | Dark content in both themes | The log viewer retains its established text contrast. |
| Source code and file comparisons | Syntax and change colors | The colors identify code and added or removed lines. |
| Setup Journal approval | Navy surface in both themes | The approval view distinguishes an image decision and preserves code contrast. |
| Historical setup files | File viewer colors for the selected theme | Historical files retain their exact source and changes. |
| OpenWiki | Viewer appearance | OpenWiki owns its content and runs behind its access boundary. |
| AgentsView | Viewer appearance | AgentsView owns its content and uses a separate browser origin. |
| Agent artifacts | Artifact content and colors | The artifact supplies its own content and appearance. |
| Standalone trace pages | System font fallback when Figtree is unavailable | The isolated page preserves its Content Security Policy and avoids an external font dependency. |

Culler controls around specialized content use the shared conventions.
Trace access pages retain nonce-protected inline styles.
The server derives their palette from the shipped dashboard stylesheet.
The server resolves only the fixed shared roles that the trace pages require.
Their dashboard origin reads the dashboard theme selection before the first paint.
Their separate origin uses its local selection or the system theme.
Neither page writes credentials or theme selections into browser storage.

## Evidence

The dashboard smoke harness supplies the actual application and a scratch database.
The convention checks cover these properties:

- Equivalent dialog headers and bodies use the required colors in both themes.
- Header text meets the required contrast.
- Field boundaries and both header button boundaries meet the required contrast.
- Role details and Environment headers preserve their shared colors and control access.
- Role drafts and text selection survive theme changes and a revision refresh.
- Long task details retain the close control at each required viewport size.
- Stacked task forms retain a lighter backdrop and the active modal focus.
- Task content remains accessible at the effective viewport of 200 percent browser zoom.
- Trace failures preserve readable colors, the reason, and the Retry action.

The complete smoke suite supplies the remaining domain behavior and page evidence.
All screenshots use the shared evidence path and the retained smoke harness.
The trace access unit tests preserve these properties:

- Safe text display.
- Nonce protection.
- Security headers.
- Each failure message.

The trace proxy tests preserve the access boundary and recovery behavior.

The zoom check uses a 720 × 450 layout viewport for a 1440 × 900 desktop at 200 percent browser zoom.

The convention and System health suites passed eleven tests with 518 assertions.
The theme suite passed three tests.
The accent contrast check passed one test.
The trace access and proxy suite passed ten tests with 139 assertions.

The visual review covered both themes for these components:

- Role instructions and their fields.
- Task details and their danger action.
- Stacked task action forms.
- Pipeline role details.
- Environment headers and their actions.
- System health headers and their status labels.
- Trace access and failure pages.

The zoom evidence covers the effective layout viewport.
It uses viewport dimensions rather than a native browser zoom command.

## Complete checks

The dashboard fast checks passed 868 tests.
The mission control checks passed.
Those checks include the release manifests and deployment contracts.
The complete light-theme browser suite passed 281 tests across 47 files.
The complete dark-theme browser suite passed 282 tests across 47 files.
Both complete smoke gates reported zero failures and zero skipped tests.

The task branch includes the latest local `main` changes.
Each theme retains its current background image.
The dashboard fast checks passed again with 868 tests and 5,237 assertions.
The deployment and test environment checks passed again with the cache disabled.
The final theme, contrast, and convention checks passed nine browser tests with 523 assertions.
Those checks reported zero failures and zero skipped tests.
