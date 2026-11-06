// The image copies this module into the pinned OpenWiki package.
import { createHash, randomUUID } from "node:crypto";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdir, readFile, rename, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { createRepositorySourceSnapshot } from "./agent/utils.js";
import { ClaimsStore } from "./claims/brains/code/store.js";
import { normalizeWikiPagePath, toClaimsSidecarRelativePath } from "./claims/brains/code/paths.js";
import { readRepositoryPageManifest } from "./generation/page-manifest.js";
import { RepositoryRunError } from "./generation/errors.js";

const execute = promisify(execFile);
const recordPath = "openwiki/.verification.json";
const digest = (bytes) => `sha256:${createHash("sha256").update(bytes).digest("hex")}`;
const emptyRecord = () => ({ format: 1, pages: {}, sources: {} });
const isDigest = (value) => typeof value === "string" && /^sha256:[a-f0-9]{64}$/.test(value);

async function bytes(root, path) {
  try { return await readFile(join(root, path)); }
  catch (error) { if (error.code === "ENOENT") return null; throw error; }
}

async function readRecord(root) {
  const raw = await bytes(root, recordPath);
  if (!raw) return emptyRecord();
  let parsed;
  try { parsed = JSON.parse(raw); }
  catch { return emptyRecord(); }
  const record = emptyRecord();
  if (parsed?.format !== 1) return record;
  for (const [page, entry] of Object.entries(parsed.pages ?? {})) {
    try { if (normalizeWikiPagePath(page) !== page) continue; }
    catch { continue; }
    if (isDigest(entry?.pageVersion) && isDigest(entry?.claimsVersion) && isDigest(entry?.sourceFingerprint)) record.pages[page] = entry;
  }
  for (const [fingerprint, files] of Object.entries(parsed.sources ?? {})) {
    if (!isDigest(fingerprint) || !files || typeof files !== "object" || Array.isArray(files)) continue;
    if (!Object.values(files).every(isDigest)) continue;
    if (Object.keys(files).some((path) => path.startsWith("/") || /[\\\x00\r\n]/.test(path) || path.split("/").includes(".."))) continue;
    record.sources[fingerprint] = files;
  }
  return record;
}

async function saveRecord(root, record) {
  const used = new Set(Object.values(record.pages).map((entry) => entry.sourceFingerprint));
  record.sources = Object.fromEntries(Object.entries(record.sources).filter(([key]) => used.has(key)));
  const target = join(root, recordPath);
  await mkdir(join(root, "openwiki"), { recursive: true });
  const temporary = `${target}.${randomUUID()}.tmp`;
  try {
    await writeFile(temporary, JSON.stringify(record, null, 2) + "\n", { flag: "wx" });
    await rename(temporary, target);
  } finally { await rm(temporary, { force: true }); }
}

async function pageFiles(root, page) {
  const markdown = await bytes(root, page.slice(1));
  const claims = await bytes(root, `openwiki/.claims/${toClaimsSidecarRelativePath(page)}`);
  return { pageVersion: markdown === null ? null : digest(markdown), claimsVersion: claims === null ? null : digest(claims) };
}

export async function compareGitSource(root, baseline, ignore) {
  if (!/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/.test(baseline ?? "")) return { known: false, reason: "The previous source commit is absent." };
  try {
    const { stdout } = await execute("git", ["-C", root, "-c", `safe.directory=${root}`, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "diff", "--name-only", "--no-renames", "-z", baseline, "--"], {
      env: { PATH: process.env.PATH, HOME: process.env.HOME, GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: "/dev/null", GIT_NO_REPLACE_OBJECTS: "1", GIT_TERMINAL_PROMPT: "0" },
      maxBuffer: 64 << 20,
    });
    const paths = stdout.split("\0").filter(Boolean).filter((path) => !path.startsWith("openwiki/") && !ignore.ignores(path));
    return { known: true, changedPaths: paths.sort() };
  } catch {
    return { known: false, reason: "Git cannot compare the previous source commit." };
  }
}

export async function recoveryWindows(root, ignore, pages) {
  const record = await readRecord(root);
  const manifest = await readRepositoryPageManifest(root);
  const current = await createRepositorySourceSnapshot(root, ignore);
  pages = [...new Set([...pages, ...Object.keys(record.pages)])].sort();
  const windows = new Map();
  const comparisons = new Map();
  for (const page of pages) {
    const entry = manifest.pages[page];
    const saved = record.pages[page];
    const baseline = entry?.gitHead;
    if (!comparisons.has(baseline)) comparisons.set(baseline, await compareGitSource(root, baseline, ignore));
    const comparison = comparisons.get(baseline);
    const previous = record.sources[saved?.sourceFingerprint];
    let changedPaths;
    let basis;
    if (previous) {
      changedPaths = [...new Set([...Object.keys(previous), ...Object.keys(current.files)])]
        .filter((path) => previous[path] !== current.files[path]).sort();
      basis = "source_inventory";
    } else if (comparison.known) {
      changedPaths = comparison.changedPaths;
      basis = "git";
    } else {
      changedPaths = [];
      basis = "unavailable";
    }
    const comparisonView = { ...(baseline ? { baseGitHead: baseline } : {}), changedPaths,
      fullReview: basis === "unavailable", basis, ...(comparison.known ? {} : { historyIssue: comparison.reason }) };
    const key = JSON.stringify(comparisonView);
    if (!windows.has(key)) windows.set(key, { pages: [], ...comparisonView });
    windows.get(key).pages.push(page);
  }
  return [...windows.values()];
}

export async function addRecoveryIssues(store, resolver, persisted, issues) {
  const record = await readRecord(store.rootDir);
  const pages = [...new Set([...await store.discoverPages(), ...await store.discoverSidecarPages(), ...Object.keys(record.pages)])].sort();
  for (const page of pages) {
    if (!persisted.has(page)) {
      const claims = await store.loadPage(page);
      if (claims) persisted.set(page, claims);
    }
  }
  const windows = await recoveryWindows(store.rootDir, resolver.openWikiIgnore, pages);
  for (const page of pages) {
    const saved = record.pages[page];
    const current = await pageFiles(store.rootDir, page);
    const claims = persisted.get(page);
    const window = windows.find((item) => item.pages.includes(page));
    // A legacy record cannot prove that nobody edited its Claim statements.
    const reason = !current.pageVersion ? "The generated page is absent."
      : !saved ? "The page lacks a complete verification record."
      : current.pageVersion !== saved.pageVersion ? "The page changed after verification."
      : current.claimsVersion !== saved.claimsVersion ? "The Claim record changed after verification."
      : !claims?.verification ? "The Claim verification is absent."
      : window.fullReview ? "The previous source cannot be compared."
      : null;
    if (!reason) continue;
    const owned = claims?.claims ?? [];
    if (!owned.length) issues.push({ page, kind: "stale", resources: [], reason });
    for (const claim of owned) {
      if (!issues.some((issue) => issue.page === page && issue.claimId === claim.id)) {
        issues.push({ page, kind: "stale", claimId: claim.id, resources: claim.evidence.map((item) => item.resource), reason });
      }
    }
  }
}

// The next accepted checkpoint binds both files. Partial writes require another review.
export async function checkpointPage(run, page) {
  const record = await readRecord(run.root);
  const source = await createRepositorySourceSnapshot(run.root, run.ignore);
  if (source.fingerprint !== run.state.sourceFingerprint) throw new RepositoryRunError("invalid_state", "The source changed. Begin a new plan.");
  const pending = new Set(run.state.plan.pages.filter((job) => job.status === "pending" && job.path !== page).map((job) => job.path));
  for (const other of Object.keys(record.pages)) {
    if (!pending.has(other) && !run.state.plan.deletePages.includes(other)) record.pages[other] = { ...record.pages[other], ...await verifiedFiles(run.root, other) };
  }
  record.sources[source.fingerprint] = source.files;
  record.pages[page] = { ...await verifiedFiles(run.root, page), sourceFingerprint: source.fingerprint };
  await saveRecord(run.root, record);
  await captureRunClaims(run);
}

export async function captureReview(run, page) {
  const currentFiles = await pageFiles(run.root, page);
  if ((run.expectedClaims?.get(page) ?? null) !== currentFiles.claimsVersion) {
    throw new RepositoryRunError("invalid_state", `The Claim record changed during the run: ${page}. Begin again to load its current Claims.`);
  }
  const persisted = await new ClaimsStore(run.root).loadPage(page);
  const expected = (persisted?.claims ?? []).map((claim) => ({ id: claim.id, statement: claim.statement, evidence: claim.evidence.map((item) => item.resource) }));
  const active = run.claimsRuntime.session.inspectClaims(page).map(({ id, statement, evidence }) => ({ id, statement, evidence }));
  if (JSON.stringify(expected) !== JSON.stringify(active)) {
    throw new RepositoryRunError("invalid_state", `The Claim record changed during the run: ${page}. Begin again to load its current Claims.`);
  }
  const current = await pageFiles(run.root, page);
  run.reviewedPages ??= new Map();
  run.reviewedPages.set(page, current);
  return current.pageVersion;
}

export async function requireReview(run, page) {
  const reviewed = run.reviewedPages?.get(page);
  if (!reviewed) throw new RepositoryRunError("invalid_state", `Inspect the Claims after the final page edit: ${page}`);
  if (JSON.stringify(reviewed) !== JSON.stringify(await pageFiles(run.root, page))) {
    throw new RepositoryRunError("invalid_state", `The page or Claim record changed after inspection: ${page}. Inspect it again before submission.`);
  }
}

export async function guardVerifiedPages(run, exceptPage) {
  const record = await readRecord(run.root);
  const pending = new Set(run.state.plan?.pages.filter((job) => job.status === "pending").map((job) => job.path));
  for (const [page, expected] of run.expectedClaims ?? []) {
    if (page === exceptPage || run.state.plan?.deletePages.includes(page)) continue;
    if ((await pageFiles(run.root, page)).claimsVersion !== expected) {
      throw new RepositoryRunError("invalid_state", `The Claim record changed during the run: ${page}. Begin again to load its current Claims.`);
    }
  }
  for (const [page, saved] of Object.entries(record.pages)) {
    if (page === exceptPage || pending.has(page) || run.state.plan?.deletePages.includes(page)) continue;
    const current = await pageFiles(run.root, page);
    if (current.pageVersion !== saved.pageVersion || current.claimsVersion !== saved.claimsVersion) {
      throw new RepositoryRunError("invalid_state", `The verified page or Claim record changed: ${page}. Begin again to review it.`);
    }
  }
}

export async function checkpointFinalPages(run) {
  const store = new ClaimsStore(run.root);
  const pages = await store.discoverPages();
  const record = await readRecord(run.root);
  for (const page of Object.keys(record.pages)) if (!pages.includes(page)) delete record.pages[page];
  for (const page of pages) {
    if (!record.pages[page]) throw new RepositoryRunError("invalid_state", `The page still requires review: ${page}`);
    record.pages[page] = { ...record.pages[page], ...await verifiedFiles(run.root, page) };
  }
  await saveRecord(run.root, record);
}

export async function hasVerifiedFiles(root, page) {
  const saved = (await readRecord(root)).pages[page];
  const current = await pageFiles(root, page);
  return !!saved && saved.pageVersion === current.pageVersion && saved.claimsVersion === current.claimsVersion;
}

export async function refreshKnownFiles(root) {
  const record = await readRecord(root);
  for (const page of Object.keys(record.pages)) record.pages[page] = { ...record.pages[page], ...await verifiedFiles(root, page) };
  await saveRecord(root, record);
}

export async function captureSubmission(run, page) {
  return pageFiles(run.root, page);
}

export async function checkSubmission(run, page, snapshot) {
  if (JSON.stringify(snapshot) !== JSON.stringify(await pageFiles(run.root, page))) {
    throw new RepositoryRunError("invalid_state", `The page or Claim record changed during submission: ${page}. Begin again to review it.`);
  }
  await guardVerifiedPages(run, page);
}

export async function captureRunClaims(run) {
  run.expectedClaims = new Map();
  const store = new ClaimsStore(run.root);
  for (const page of new Set([...await store.discoverPages(), ...await store.discoverSidecarPages()])) {
    run.expectedClaims.set(page, (await pageFiles(run.root, page)).claimsVersion);
  }
}

async function verifiedFiles(root, page) {
  const files = await pageFiles(root, page);
  const claims = await new ClaimsStore(root).loadPage(page);
  if (!claims?.verification || claims.pageVersion !== files.pageVersion || !files.claimsVersion) {
    throw new RepositoryRunError("invalid_state", `The page and Claim record require another review: ${page}`);
  }
  return files;
}
