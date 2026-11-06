// The package hash guard validates each source before these replacements.
export const recoveryPatches = {
  "generation/page-manifest.js": [
    ["throw new RepositoryRunError(\"invalid_state\", `OpenWiki page manifest is malformed at ${file}; refusing to discard committed page coverage.`);", "return createEmptyRepositoryPageManifest();"],
    ["import { randomUUID }", `import { hasVerifiedFiles } from "../culler-recovery.js";
import { randomUUID }`],
    [`    if (!source.sourceFingerprint)
        return null;`, `    if (!source.sourceFingerprint || !await hasVerifiedFiles(root, page))
        return null;`],
  ],
  "claims/brains/code/preflight.js": [
    ["        orphanPages,", "        orphanPages: orphanPages.filter((page) => !persisted.has(page)),"],
    ["import { EvidenceSecurityError }", `import { addRecoveryIssues } from "../../../culler-recovery.js";
import { EvidenceSecurityError }`],
    ["    issues.sort(compareGroundingIssues);", `    await addRecoveryIssues(store, resolver, persisted, issues);
    issues.sort(compareGroundingIssues);`],
  ],
  "agent/utils.js": [
    [`    for (const sourcePath of visiblePaths) {
        await updateFingerprintSourceEntry(hash, cwd, sourcePath, trackedPaths.has(sourcePath));
    }`, `    const files = Object.create(null);
    for (const sourcePath of visiblePaths) {
        const fileHash = createHash("sha256");
        const both = { update(bytes, encoding) { hash.update(bytes, encoding); fileHash.update(bytes, encoding); } };
        await updateFingerprintSourceEntry(both, cwd, sourcePath, trackedPaths.has(sourcePath));
        files[sourcePath] = \`sha256:\${fileHash.digest("hex")}\`;
    }`],
    [`        fingerprint,
        ...(head.startsWith`, `        fingerprint,
        files,
        ...(head.startsWith`],
  ],
  "generation/repository-run.js": [
    ["`OpenWiki cannot finish with ${pending.length} pending page job(s).`", "`OpenWiki has pending page reviews: ${pending.map(({ path }) => path).join(\", \")}.`"],
    ["async function toActiveBeginView(run, resumed) {", `async function toActiveBeginView(run, resumed) {
    await captureRunClaims(run);`],
    [`    return withRunMutation(run, async () => {
        // Re-read shared state:`, `    const submission = await captureSubmission(run, current.path);
    return withRunMutation(run, async () => {
        // Re-read shared state:`],
    ["            await run.claimsRuntime.finalize(run.state.startedAt, otherPendingPages);", `            for (const deleted of latestPlan.deletePages) otherPendingPages.add(deleted);
            await checkSubmission(run, current.path, submission);
            await run.claimsRuntime.finalize(run.state.startedAt, otherPendingPages);`],
    ["                    if (publishedSource.fingerprint === source.fingerprint) {", `                    if (publishedSource.fingerprint === source.fingerprint) {
                        await refreshKnownFiles(input.root);`],
    ["import { randomUUID }", `import { recoveryWindows, checkpointPage, captureReview, requireReview, guardVerifiedPages, checkpointFinalPages, refreshKnownFiles, captureSubmission, checkSubmission, captureRunClaims } from "../culler-recovery.js";
import { randomUUID }`],
    ["export function inspectRepositoryPageClaims(run, jobId) {", "export async function inspectRepositoryPageClaims(run, jobId) {"],
    ["        claims: run.claimsRuntime.session.inspectClaims(current.path),", `        pageVersion: await captureReview(run, current.path),
        claims: run.claimsRuntime.session.inspectClaims(current.path),`],
    ["    const frontmatter = await repairPersistedFile(run.backend, current.path,", `    await requireReview(run, current.path);
    await guardVerifiedPages(run, current.path);
    const frontmatter = await repairPersistedFile(run.backend, current.path,`],
    ["        await recordRepositoryPageCompletion(run.root, current.path, getRepositoryRunSourceCheckpoint(run.state), run.state.actor.producerActor, run.state.runId);", `        await recordRepositoryPageCompletion(run.root, current.path, getRepositoryRunSourceCheckpoint(run.state), run.state.actor.producerActor, run.state.runId);
        await checkpointPage(run, current.path);`],
    ["    const pending = plan.pages.filter(({ status }) => status === \"pending\");", `    await guardVerifiedPages(run);
    const pending = plan.pages.filter(({ status }) => status === "pending");`],
    ["    // Delete this LAST. If anything above fails, begin() can reconstruct and retry.", `    await checkpointFinalPages(run);
    // Delete this LAST. If anything above fails, begin() can reconstruct and retry.`],
    [`async function getRepositoryPageUpdateWindows(root, ignore, pages) {
    const manifest = await readRepositoryPageManifest(root);
    const pagesByBaseline = new Map();
    for (const page of pages) {
        const baseline = manifest.pages[page]?.gitHead ?? "";
        const group = pagesByBaseline.get(baseline) ?? [];
        group.push(page);
        pagesByBaseline.set(baseline, group);
    }
    const windows = [];
    const baselines = [...pagesByBaseline.keys()].sort(compareCodeUnits);
    for (const baseline of baselines) {
        windows.push({
            ...(baseline ? { baseGitHead: baseline } : {}),
            pages: [...(pagesByBaseline.get(baseline) ?? [])].sort(compareCodeUnits),
            changedPaths: await getRepositoryChangedPaths(root, ignore, baseline || undefined),
            fullReview: baseline.length === 0,
        });
    }
    return windows;
}`, `async function getRepositoryPageUpdateWindows(root, ignore, pages) {
    return recoveryWindows(root, ignore, pages);
}`],
    [`        if (input.mode === "update") {
            await fastForwardUnchangedRepositoryPageCoverage(input.root, ignore, initialPages, {
                sourceFingerprint: source.fingerprint,
                ...(source.gitHead ? { gitHead: source.gitHead } : {}),
            });
        }
`, ""],
    [`async function fastForwardUnchangedRepositoryPageCoverage(root, ignore, pages, source) {
    if (!source.gitHead)
        return;
    const manifest = await readRepositoryPageManifest(root);
    for (const page of [...pages].sort(compareCodeUnits)) {
        const entry = manifest.pages[page];
        if (!entry?.gitHead || entry.gitHead === source.gitHead)
            continue;
        const changedPaths = await getRepositoryChangedPaths(root, ignore, entry.gitHead);
        if (changedPaths.length > 0)
            continue;
        await recordRepositoryPageCompletion(root, page, source, entry.completedBy, entry.completedRunId);
    }
}`, ""],
    [`        if (page.status === "complete" && !current) {
            // Deterministic finish may rewrite a completed page and its sidecar before
            // a later failure leaves \`.run.json\` in place. Re-prove those exact bytes
            // instead of rejecting a safely resumable finish. Unpaired edits still
            // fail closed inside recordRepositoryPageCompletion.
            current = await recordRepositoryPageCompletion(root, page.path, source, page.completedBy ?? legacyCompletedBy, state.runId);
        }
`, `        if (page.status === "complete" && !current) {
            pages.push({ ...page, status: "pending" });
            changed = true;
            continue;
        }
`],
    [`    const run = {
        root: input.root,
        state: activeState,`, `    if (activeState.plan) {
        const required = createRepositoryPlan("update", { pages: [], deletePages: activeState.plan.deletePages }, claimsRuntime.issues);
        const byPath = new Map(activeState.plan.pages.map((job) => [job.path, job]));
        for (const job of required.pages) {
            const old = byPath.get(job.path);
            byPath.set(job.path, old ? { ...old, status: "pending" } : job);
        }
        activeState = { ...activeState, plan: { ...activeState.plan, pages: [...byPath.values()] } };
        await writeRepositoryRunState(input.root, activeState);
    }
    const run = {
        root: input.root,
        state: activeState,`],
  ],
  "claims/brains/code/store.js": [
    ["throw new ClaimsPersistenceError(`Invalid JSON in ${this.displayPath(sidecar)}: ${toErrorMessage(error)}`);", "return null;"],
    ["return validatePageClaims(parsed, `claim sidecar ${this.displayPath(sidecar)}`);", `try { return validatePageClaims(parsed, \`claim sidecar \${this.displayPath(sidecar)}\`); }
        catch (error) { if (error instanceof ClaimsPersistenceSecurityError) throw error; return null; }`],
  ],
};
