# OpenWiki integration

Culler uses OpenWiki 0.7.0 through its six host tools.
The package lock identifies the complete dependency set and package integrity.
The runner image installs that set without package scripts.
Node 24.18.0 supplies the runtime.

The Scribe's harness supplies model inference through its assigned provider binding.
The local Model Context Protocol (MCP) server receives an empty environment except for `PATH` and its scratch `HOME`.
The server receives the repository path and exact input commit as arguments.
The server uses the OpenWiki host modules without the standalone model agent.

`patch-upstream.mjs` contains the explicit changes to the fixed upstream package.
These changes have these purposes:

- Culler controls repository setup and automatic wiki work.
- The server supplies instructions for the six Scribe tools.
- Initialization preserves project documents and the user's wiki brief.
- Project documents remain outside generated Claim state.
- Generated page operations preserve complete project documents.
- A source change prevents successful finalization.

The integration owns `openwiki/.culler-run.json` during page work.
OpenWiki owns its page queue, Claim records, and other generated records.
Both sets of records follow the repository's normal Git continuity contract.
The integration removes its run record after successful final checks.

A wiki update runs on a schedule, outside each task.
A wiki update is one Scribe run for one Project, together with the merge of its wiki commit into `main`.
The accepted commit is the commit at the `main` branch of the Project repository.
The start commit is the accepted commit at the time that `mc` creates the run.
The start commit is the assigned input commit.
The [dispatch guide](../../mc/dispatch/README.md) states each rule of a wiki update.

These terms name the wiki files:

- The Project wiki is the `openwiki/` folder of the Project repository.
- A control record is the file `openwiki/.last-update.json` or the file `openwiki/.page-manifest.json`. OpenWiki writes both files in each run.
- A generated page is an explanation that the Scribe writes through OpenWiki.
- A Claim is one fact that a generated page states, together with the source lines that prove the fact.
- A Claim sidecar is the file under `openwiki/.claims/` that holds the Claims of one generated page.
- A navigation page is an `index.md` file under `openwiki/`. OpenWiki writes each navigation page.
- A project document is an original repository deliverable, such as a research report or specification.

Only the Scribe maintains `openwiki/`, including its control records, Claim sidecars, generated pages, and navigation pages.
Every other pipeline role treats the wiki as read-only.
The Worker commits original project documents outside the wiki through the Project's document conventions.
The default document location is `docs/`.
The task's requested format takes precedence over the default text format, Markdown.
Agents do not combine individual fields from OpenWiki control records.

Project documents follow the normal Git content merge of their task.
The Worker resolves source and project document conflicts in that task.
A merge conflict of a wiki commit ends the wiki update as failed.
The next wiki update does the wiki work again from the accepted commit.

An imported wiki can contain original Markdown documents.
These documents declare `document: true`, a `title`, and a `type` in their frontmatter.
They can also identify their original task through `task_id` and `task_status`.
Normal page work preserves their complete bytes.
A generated page with a Claim sidecar retains its Claim checks.

Original project documents supply evidence at their authoritative repository paths.
Generated pages explain original documents outside `openwiki/` and cite their authoritative paths.
Imported original documents retain their established paths and formats.
Generated explanations require original evidence and cannot substantiate each other through a cycle.
Wiki metadata repairs enter a commit before generated page work.
Original documents remain fixed throughout that work.

OpenWiki reserves every `index.md` path for generated navigation.
Imported original documents use other filenames.
The host rejects a project document at a reserved path before repository mutations.
A wiki update fails while a project document occupies a reserved path.
The wiki update reports the incompatible document and preserves its content.

The completion receipt identifies these facts:

- The assigned input commit: the start commit of the wiki update.
- The source baseline commit.
- The OpenWiki package version and integrity.
- The successful finish or no-op result.
- The SHA-256 digest of every wiki file.

The production receipt resides at `/tmp/openwiki-completion.json`.
The receipt contains metadata and uses mode `0644`.
This mode permits the terminal process to read it under its separate user identity.
`mc complete --run <run_id> --commit <full-commit> --wiki-evidence <path>` submits the wiki commit and the receipt.
The terminal validates the receipt against the submitted Git objects.
The `wiki_updates` row holds the start commit and the wiki commit.

The local MCP server writes the receipt in the container of the Scribe run.
The Scribe can write the same file.
The receipt thus protects against an incomplete OpenWiki run of an honest Scribe.
The receipt is no protection against a hostile Scribe.

`mc` accepts a wiki commit only when each of these checks passes:

- The start commit is an ancestor of the wiki commit.
- The wiki commit passes the scope check against the start commit.
- The receipt names the start commit, a complete OpenWiki run, and the pinned OpenWiki version.
- The digest of the wiki files of the wiki commit equals the digest in the receipt.
- The wiki commit holds no record of an incomplete OpenWiki run.

The scope check has these rules for a wiki commit and its start commit:

- Each path that the wiki commit changes is under `openwiki/`.
- `openwiki/INSTRUCTIONS.md` is the same as in the start commit.
- Each project document is the same as in the start commit, except the fields `title`, `type`, `document`, and `tags`.
- Each project document of the wiki commit is a project document of the start commit.
- Each entry under `openwiki/` of the start commit and of the wiki commit is a regular file.
- Each path under `openwiki/` of those two commits holds no backslash and no line break.
- Each wiki file that the wiki commit changes has no executable permissions.

A failed check ends the wiki update as failed.
An entry under `openwiki/` that is no regular file is, for example, a symbolic link or a submodule.
The cause of the failed wiki update names the commit and the entry.
A wiki update fails while the start commit holds such an entry.
A task can remove the entry.

A Git failure during a check of the terminal gives no check result.
`mc` then refuses the command and changes no state.

A `noop` result with no changed file needs no commit, and the Scribe then submits the start commit.
An accepted wiki commit that equals the start commit is a successful wiki update with no merge.
Culler merges each other accepted wiki commit into `main` with no review packet and no operator decision.

The merge candidate step applies the scope check to the merge candidate of the wiki commit.
There, the merge candidate replaces the wiki commit, and the destination commit of the merge candidate replaces the start commit.
The terminal reads the repository of the Scribe run, and the Scribe writes that repository.
The merge candidate step reads its own temporary repository, and no agent writes that repository.
A failed scope check of the merge candidate ends the wiki update as failed, and `main` stays unchanged.
The scope check of the merge candidate uses no receipt.

The Docker-free compatibility tests use the real package without model calls.
Their dependencies require `npm ci --ignore-scripts --no-audit --no-fund` in this directory.
`./check.sh` runs those tests through the repository's fixed Bun version.
The runner image check includes this suite.
The explicit live acceptance test covers authenticated sessions separately.

The OpenWiki viewer is the page that the OpenWiki export command writes for one `openwiki/` tree.
Culler keeps one wiki export for each Project whose accepted commit has an `openwiki/` tree.
The export command reads the `openwiki/` tree of the accepted commit, so uncommitted files cannot change the export.
After the merge of a wiki commit, the export shows the wiki of the new accepted commit.
The command runs in the runner image without network access or credentials.

The dashboard process serves each wiki export at the wiki address.
`[surfaces.wiki] bind` in `MC_HOME/config.toml` sets the wiki address. The default address is `127.0.0.1:7335`.
The wiki address is a separate browser origin from the dashboard address. It requires no dashboard token.
The wiki address serves each export at `/<wiki key>/<project ID>/`. It accepts only read requests.
The wiki key is a secret path prefix in `MC_HOME/state/wiki-key`. Each path without the wiki key gives 404.
The dashboard header and the Project menu link to the OpenWiki viewer of each Project.
The export page loads its four browser libraries from a public content delivery network.
The wiki address serves local copies of those libraries below the wiki key in place of the network copies.
The local copies, their licenses, and their provenance reside under `dashboard/src/openwiki/`.
The wiki address removes the Google Fonts links, so the page uses the system fonts.
The OpenWiki viewer receives no task controls and no credentials.

Culler keeps task execution artifacts under `.mc-artifacts/` outside Git.
The repository contains complete deliverables and their necessary local evidence.
The Worker selects document locations outside `openwiki/` from project conventions, with `docs/` as the default.
The Scribe preserves the body of each project document and each file outside `openwiki/`.
It maintains metadata and navigation for original documents in imported wikis before generated page work.
The next scheduled wiki update explains relevant project documents through generated pages that cite the original files.

Task artifacts expire 90 days after task closure and the end of active dependencies.
Active reviews and incomplete repository cleanup preserve their artifacts.
The resident requests expiry through Culler before each dispatch tick.
The spine reserves each expiry operation before the host removes artifact files.
An interrupted expiry resumes with the same reservation.
Task history records the expiry date and preserves task decisions.

The upstream interfaces are documented in the [OpenWiki repository](https://github.com/langchain-ai/openwiki).
