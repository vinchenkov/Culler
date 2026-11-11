import { dialogHeader, dialogBody, dialogFooter, dialogFrame, showDialog } from "./components.js";
import { taskRuns } from "./board.js";
import { createRoleCustomization, customizationRole } from "./customization.js";
import { createEvaluationControl } from "./evaluation.js";
import { createRequests, isReadRequest } from "./requests.js";
import { systemIssues, affectedProposalNodes, createSystemStatus, createDeploymentCheck, connectionGroups, connectionDisclosureLabel, systemActionButton, systemConditionNodes, systemNoticeNode, actionControl, repairText, conditionSummary, conditionImpact, conditionFacts, profileName, conditionExplanation, conditionRetry, diagnosticResults, refreshSummary, releaseView } from "./systemhealth.js";
// The dashboard shell reads command results and renders the visible page.
// Each project page uses the selected Project.

import { isDockerUnavailable } from "./errors.js";
import { createDiscordPanel } from "./discord.js";
import { hostPaths } from "./paths.js";
import { packetHash, packetRoute, parseRoute, routeHash } from "./router.js";
import { parseMarkdown } from "./markdown.js";
import { el, markdownNodes, svg, tag, reconcileChildren } from "./dom.js";
import { connectionName, projectName, leaseStateText, pauseAllView, projectStateText, stampLabel, agoLabel } from "./format.js";
import { taskDialogBody, taskDialogHead } from "./taskdialog.js";
import { boardHistoryNode, boardRecordControls, createBoardWrites } from "./boardwrites.js";
import { boardColumns, boardStatItems, boardStats, leaseBadge, taskDetail, visionNotices } from "./board.js";
import {
  conversationName,
  composeState,
  collieListed,
  pollTight,
  provisionalRow,
  tabOpenAction,
  sessionRows,
  sessionStats,
  transcriptRows,
} from "./colliefmt.js";
import {
  FRAME_SANDBOX,
  decisionPayload,
  firstPacket,
  LEDGER_FILTERS,
  ledgerEmptyText,
  packetView,
  packetsFor,
  queueStats,
  readerPosition,
  reasonRequired,
  renderMode,
  reportArea,
  reportDocument,
  setFrameFont,
} from "./review.js";
import { changesLabel } from "./changes.js";
import { createChangeView } from "./changeview.js";
import { mountScene, mountTheme } from "./theme.js";
import { mountSceneMotion } from "./scene.js";
import { activityRows, kindOptions, mergeRows, newRowIDs, splitStreams, streamStats } from "./activity.js";
import { degradedUnits, doctorView, healthView, outboxLagView, runsView } from "./pipeline.js";
import { topologyView } from "./topology.js";
import { flowRunNode, flowPopoverNode, flowSideNode, flowWikiNode, flowmapNode, flowmapView, layoutFor } from "./flowmap.js";
import { createAgentProfiles } from "./agentprofiles.js";
import { createProjectControls, systemExplanation, agentReadinessNode, projectState } from "./projects.js";
import { createProjectDeletion } from "./projectdelete.js";
import { createMessageRenderer, createConversation, createComposer, createModelPicker, conversationEmpty } from "./conversation.js";
import { boardMessageText, createCommandBar } from "./commandbar.js";
import { createPacketChat, createPacketChatResize } from "./packetchat.js";
import { createDiagnosticChat, diagnosticButton, conditionDiagnostic, taskDiagnostic } from "./diagnosticchat.js";
import { createVision } from "./vision.js";
import { createEnvironment } from "./environment.js";
import { setupJournalModel, setupJournalNode, acceptSetupHealth, initialSetupComplete } from "./setup-journal.js";
import {
  applyViewerLink, projectWikiLinkView, traceLinkNode, tracesLink, viewerLinkNode, wikiLink,
} from "./viewers.js";
import { configGroups, configInputValue, configStoredValue, routingRows, projectRows } from "./configs.js";
import {
  LOG_LEVELS,
  LOG_STREAMS,
  RUN_LANE,
  UNIT_OPTIONS,
  streamsFor,
  TAIL_DEFAULT,
  canLoadOlder,
  copyText,
  levelCounts,
  logsHash,
  logsQuery,
  logsView,
  nextTail,
  parseLogsHash,
  runOptions,
  processRuns,
} from "./logs.js";

const TIGHT_MS = 1000;
const LAZY_MS = 10000;
// The header's lease badge is live on every page, but it is a spine read like
// any other: it keeps its own lazy cadence so a tight Collie poll cannot
// turn one `mc lease list` + `mc settings get` per ten seconds into one per
// second.
const LEASE_MS = 10000;
// How many merged activity rows the live tail keeps on screen.
const TAIL_CAP = 500;
// How many rows to ASK the verb for. This is not cosmetic: the page splits one
// unfiltered read into the event and error streams, so whatever the read misses
// the error pane reports as "nothing has refused". At the verb's 100-row default
// a busy deployment returns a hundred dispatch.spawn rows and the error pane
// lies. 1000 is the verb's own clamp — ask for all of it.
const ACTIVITY_LIMIT = 1000;

let visionRead = null;
let environmentRead = null;

const state = {
  tab: "board",
  // Every lease row (one per Project) and the one settings row, read
  // together: the badge judges staleness by the settings' heartbeat interval.
  leases: [],
  settings: null,
  pause: null,
  pauseBusy: false,
  pauseReadAt: 0,
  pauseSerial: 0,
  pauseError: "",
  leaseReadAt: 0,
  leaseError: "",
  dockerUnavailable: false,
  project: null,
  scopeSerial: 0,
  projectReady: false,
  /** The task the dialog shows, by id, or null while it is closed. Browser
   * state only: a poll re-renders the board and the dialog stays open. */
  boardOpen: null,
  /** The open task's `mc task get` row (or the read's error), once read. */
  boardDetail: null,
  sessions: [],
  sessionsRead: false,
  selected: null,
  packets: [],
  packetView: "live",
  ledgerFilter: "approved",
  /** The task of the open reader, or null while the packet list shows. */
  selectedPacket: null,
  /** The task that the address names before the packet list is read. */
  readerWanted: null,
  /** Whether the Review page opens the first packet of the list once it reads
   * the list: after the operator opens the page or selects another Project. */
  readerAuto: false,
  /** The last packet row of the open reader, and the packet version it shows. */
  readerRow: null,
  readerKey: null,
  readerPlace: null,
  /** The history length after this page pushed the current reader entry, or 0
   * when the reader entry came from an address. */
  readerPushed: 0,
  /** The serial of a move to another packet that returns to the list entry
   * first, or 0. */
  readerStepping: 0,
  /** The content of the reader: "packet" or "changes". */
  readerView: "packet",
  /** Whether the quick chat of the Review page is open in this browser. */
  chatOpen: false,
  readerError: { action: "", page: "" },
  selectedReviewIdentity: null,
  selectedReviewLoaded: false,
  packetLoadSerial: 0,
  pendingDecision: null,
  /** The task of the decision that the page sends, or null. */
  decisionBusy: null,
  approvalBusy: null,
  activityRead: false,
  activityRows: [],
  activityErrors: [],
  activityStream: "events",
  activityKind: "",
  activityTail: true,
  activitySerial: 0,
  activityRevision: 0,
  doctor: null,
  doctorPending: false,
  logsUnit: UNIT_OPTIONS[0],
  logsStream: LOG_STREAMS[0],
  logsTail: TAIL_DEFAULT,
  logsGrep: "",
  /** The run the run-diagnostics lane is reading, once one is picked. */
  logsRun: null,
  /** The run picker's option rows (verb list ⋈ spine rows), newest first. */
  logsRuns: [],
  /** The level facet over a structured window. Full = no facet. */
  logsLevels: new Set(LOG_LEVELS),
  logsLast: null,
  logsLastRaw: null,
  logsProcess: "resident",
  logsProcessRecords: [],
  focusReason: false,
  /** True while the open Collie session is active and owes a reply. */
  pollTight: false,
  sendInFlight: false,
  collieSessionBusy: false,
  collieReplyBusy: false,
  collieRevision: 0,
  collieHistorySession: null,
  collieHasMessages: false,
  /** True while the view waits for a resume. A tab open that lands during
   * a resume requests no other one. */
  collieResumeInFlight: false,
  /** True from a tab open until the next Collie refresh consumes it. */
  collieOpened: false,
  timer: null,
  // artifact-target → host root, fetched once. It translates the container
  // paths a packet render cites (the Packager authors inside a container and
  // can name no other path).
  artifactRoots: null,
};

const $ = (id) => document.getElementById(id);

/** The last status view (systemhealth.js): conditions, connections, and the
 * viewer facts. */
let systemView = { snapshot: null, viewers: null, unavailable: false, pending: true };
/** The last recent-run rows, so a change of the viewer facts can repaint the
 * trace links without a read. */
let pipelineRunRows = null;

// -------------------------------------------------------------- DOM plumbing

// `el` and `tag` come from dom.js; every node this file renders goes through
// them, so no pane ever assembles a string of markup.
// A stat leads with its number; the label follows as a caption.
const stat = (label, value, { key = null, title = null, lead = false, zero = false, alert = false } = {}) =>
  el("span", { class: `stat${lead ? " stat-lead" : ""}${zero ? " stat-zero" : ""}${alert ? " tone-rust" : ""}`, testid: key && `stat-${key}`, title },
    el("b", { text: String(value) }), el("span", { class: "stat-label", text: label }));
// Placeholder cards that hold the shape of a Board column until task data arrives.
const skeletonCards = () => [0, 1].map(() => el("div", { class: "skeleton-card", testid: "board-skeleton-card", "aria-hidden": "true" }));
// A placeholder states its kind: "loading", "failed", or "empty".
const emptyBox = (text, tall = false, kind = "empty") => kind === "loading"
  ? el("div", { class: "loading", role: "status", text })
  : el("div", { class: `empty${kind === "failed" ? " failed" : ""}${tall ? " tall" : ""}`, text });

// Update the child nodes and omit absent content.
function setChildren(node, ...children) {
  reconcileChildren(node, children.flat(2).filter(c => c !== null && c !== undefined && c !== false));
}

// An empty list has an explicit state.
function fill(container, nodes, empty) {
  if (nodes.length === 0) {
    setChildren(container, typeof empty === "string" ? emptyBox(empty) : empty);
    return;
  }
  setChildren(container, ...nodes);
}

/** Re-renders a scrolling stream without moving what the operator is reading:
 * a live tail that jumps the viewport every poll is unreadable. */
function keepScroll(container, render) {
  const atTop = container.scrollTop <= 4;
  const before = container.scrollHeight;
  render();
  if (atTop) {
    container.scrollTop = 0;
    return;
  }
  container.scrollTop += container.scrollHeight - before;
}

// ---------------------------------------------------------------------- auth

// Every deployment requires a token, loopback included. The setup link
// supplies it once via the URL fragment (#token=…); the browser keeps it in
// localStorage, so every later tab and restart on this browser opens the
// plain address. The fragment never leaves the browser.
const TOKEN_KEY = "mc-dashboard-token";
const hashToken = new URLSearchParams(location.hash.slice(1)).get("token");
if (hashToken) {
  try {
    localStorage.setItem(TOKEN_KEY, hashToken);
  } catch {
    // Storage blocked: the token still serves this load.
  }
  history.replaceState(null, "", location.pathname);
}
let token = hashToken;
if (!token) {
  try {
    token = localStorage.getItem(TOKEN_KEY);
  } catch {
    token = null;
  }
}

/** What the operator reads when this browser holds no token, or a stale one.
 * The server's own 401 text names the mechanism; the operator needs the
 * remedy. */
const NO_TOKEN =
  "This browser has no dashboard token. Open the dashboard link from setup once; after that the plain address works here.";

/** A fetch that fails before any response is the server being gone, not an
 * error envelope; name that for the operator instead of leaking the browser's
 * own "Failed to fetch". */
const UNREACHABLE = "dashboard server unreachable — retrying on the next poll";

const requests = createRequests({
  send: sendRequest,
  freshness: path => path.startsWith("/api/agent-profiles") || path === "/api/doctor" ? 0 : path === "/api/system/status" ? 5000
    : path.startsWith("/api/sessions") && state.pollTight ? TIGHT_MS : LAZY_MS,
  denied: () => {
    document.body.classList.add("access-denied");
    for (const dialog of document.querySelectorAll("dialog[open]")) dialog.close();
    showError(new Error(NO_TOKEN));
  },
});
mountTheme({ button: $("theme-button") });
mountScene({ button: $("scene-button"), motion: mountSceneMotion({ stage: $("scene") }) });

// The report frames take the dashboard typeface as an inline font.
fetch("/fonts/figtree.woff2")
  .then((response) => (response.ok ? response.blob() : null))
  .then((blob) => blob && new Promise((resolve) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => resolve("");
    reader.readAsDataURL(new Blob([blob], { type: "font/woff2" }));
  }))
  .then((url) => { if (url) setFrameFont(url); })
  .catch(() => {});

async function api(path, opts) {
  let result;
  try { result = await requests.request(path, opts); }
  catch (error) {
    if (path === "/api/projects" && !opts?.method) {
      projectReadError = error.message;
      projectReadAvailable = false;
      state.pause = null;
      state.pauseError = error.message;
      state.pauseReadAt = 0;
      state.projectReady = false;
      boardProjects = [];
      boardTasks = [];
      boardPackets = [];
      resetProjectView();
      renderPipelinePause();
      showError(error, { read: true });
    }
    throw error;
  }
  if (!isReadRequest(path, opts) && !path.startsWith("/api/outbox/")) {
    state.pauseReadAt = 0;
    state.leaseReadAt = 0;
  }
  return result;
}
window.addEventListener("storage", event => {
  if (event.key === TOKEN_KEY) { requests.invalidate(); location.reload(); }
});

async function sendRequest(path, opts = {}) {
  const { bytes = false, signal, ...options } = opts;
  const controller = new AbortController();
  const deadline = isReadRequest(path, options)
    ? setTimeout(() => controller.abort(), path === "/api/doctor" ? 30000 : 15000) : null;
  const headers = { ...(options.headers ?? {}) };
  const release = document.querySelector('meta[name="culler-release"]')?.content;
  if (release) headers["x-culler-release"] = release;
  if (options.body) headers["content-type"] ??= "application/json";
  if (token) headers.authorization = `Bearer ${token}`;
  try {
    const res = await fetch(path, { ...options, headers, signal: signal ? AbortSignal.any([signal, controller.signal]) : controller.signal });
    if (!res.ok) {
      const body = await res.json().catch(() => ({}));
      const error = new Error(apiErrorMessage(res.status, body));
      error.status = res.status;
      throw error;
    }
    return bytes ? { body: await res.text(), type: res.headers.get("content-type") ?? "" } : await res.json();
  } catch (error) {
    if (error.status) throw error;
    throw new Error(controller.signal.aborted ? "The request exceeded its deadline. Retry the read." : error instanceof TypeError ? UNREACHABLE : error.message || UNREACHABLE);
  } finally { clearTimeout(deadline); }
}

function apiErrorMessage(status, body) {
  if (status === 401) return NO_TOKEN;
  return body?.error?.message ?? `request failed (${status})`;
}

/** The same call for an endpoint that answers with bytes rather than JSON,
 * plus the response's own content type, for the one reader that picks its
 * rendering from it (the packet reader); failures still arrive as the error
 * envelope, so they are read as JSON. */
function apiBytes(path) {
  return api(path, { bytes: true, maxAge: 0 });
}

const DOCKER_UNAVAILABLE = "Docker is unavailable. Open Docker Desktop and wait for Docker to start.";

/** Below 960 px the Review reader covers the window. */
const narrowReader = window.matchMedia("(max-width: 959px)");

/** Whether the open reader covers the page. */
const readerCovers = () => state.selectedPacket !== null && narrowReader.matches;

// A page read clears only the error of a page read that started before it.
// The error of an operator action remains until a later action or page change.
let errorSerial = 0;
let errorFromRead = false;

function showError(err, { read = false } = {}) {
  errorSerial++;
  errorFromRead = read;
  if (document.body.classList.contains("access-denied")) err = new Error(NO_TOKEN);
  if (isDockerUnavailable(err)) {
    state.dockerUnavailable = true;
    $("docker-error-details").textContent = String(err instanceof Error ? err.message : err);
    $("docker-error").hidden = false;
    clearError();
    renderBoard();
    renderHeader();
    if (readerCovers()) {
      state.readerError.page = DOCKER_UNAVAILABLE;
      renderReaderError();
    }
    return;
  }
  const message = String(err instanceof Error ? err.message : err);
  // A reader that covers the page covers the page error box, so the reader
  // shows the error.
  if (readerCovers()) {
    state.readerError.page = message;
    renderReaderError();
    return;
  }
  $("error-message").textContent = message;
  $("error").hidden = false;
}

function clearError() {
  if (document.body.classList.contains("access-denied")) return;
  $("error").hidden = true;
  if (state.readerError.page !== "") {
    state.readerError.page = "";
    renderReaderError();
  }
}

$("error-close").addEventListener("click", clearError);

// A pane confirms only the records it displayed.
function confirmPaneDelivery(kind, ids) {
  if (!ids.length) return;
  api(`/api/outbox/${kind}/ack`, { method: "POST", body: JSON.stringify({ ids }) }).catch(() => {});
}

// -------------------------------------------------------------------- header

/** The header shows the leases or the Docker error. */
function renderHeader() {
  const now = Date.now();
  const scoped = PROJECT_PAGES.has(state.tab);
  const paused = new Set((state.pause?.projects ?? []).filter((row) => row.status === "paused").map((row) => row.id));
  const badge = state.dockerUnavailable
    ? { held: 0, stale: true, text: "Docker is unavailable", title: "Open Docker Desktop and wait for Docker to start." }
    : !state.leaseReadAt || (scoped && !state.projectReady)
      ? { held: 0, stale: false, text: state.pauseError && scoped ? "Project state unavailable" : state.leaseError ? "Lease state unavailable" : "Loading lease state…", title: state.leaseError }
      : leaseBadge(scoped ? ownRows(state.leases) : state.leases, now, state.settings,
        { allProjects: !scoped, paused, titles: new Map((state.pause?.projects ?? []).map((row) => [row.id, projectName(row.id, state.pause?.projects)])) });
  const setup = scoped && showSetupJournal() && !state.dockerUnavailable && state.projectReady;
  const setupModel = setup ? setupJournalModel(boardEnvironment, {
    project: state.pause?.projects.find(row => row.id === state.project),
    unavailable: Boolean(boardEnvironmentError), health: setupHealth,
  }) : null;
  const setupNeedsAction = !boardEnvironmentError && !setupModel?.historicalEvidenceMissing && !setupModel?.completed
    && setupModel?.attention;
  const pauseText = !setup && scoped && !state.dockerUnavailable ? selectedPauseText() : "";
  const node = $("lease-badge");
  node.classList.toggle("free", badge.held === 0);
  node.classList.toggle("running", !setup && badge.held > 0 && !badge.stale);
  node.classList.toggle("setup-progress", Boolean(setupModel?.progressing));
  node.classList.toggle("stale", setup ? setupNeedsAction : badge.stale);
  node.classList.toggle("paused", Boolean(pauseText));
  node.classList.toggle("proposal-review", Boolean(badge.proposalReview));
  node.querySelector(".text").textContent = setup
    ? boardEnvironmentError ? "Setup status unavailable" : setupModel.historicalEvidenceMissing ? "Setup evidence unavailable"
      : setupNeedsAction ? "Setup needs your attention" : setupModel.state
    : pauseText ? `${pauseText} · ${badge.text}` : badge.text;
  node.title = setup ? "Project setup runs before task development." : state.leaseError || badge.title;
  if (state.leaseError && state.leaseReadAt) node.querySelector(".text").textContent += " · Last known state";
}

/** The pause state of the selected Project, or "" when it has none. */
function selectedPauseText() {
  const row = state.pause?.projects.find((ws) => ws.id === state.project);
  if (!row || row.status === "archived") return "";
  if (row.status !== "paused") return "";
  return projectStateText(row.status, row.setup_status);
}

const PROJECT_PAGES = new Set(["board", "review", "pipeline"]);
try { state.project = localStorage.getItem("mc.project"); } catch {}

function validProject(scope) {
  if (scope !== state.scopeSerial) return false;
  if (!state.projectReady) throw new Error(state.pauseError || "The Project registry is unavailable.");
  return true;
}

function ownRows(rows) {
  return state.projectReady ? (rows ?? []).filter((row) => row.project === state.project) : [];
}

function emptyProjectRegistry() {
  return state.projectReady && state.pause?.projects.length === 0;
}

function resetProjectView(invalidate = true) {
  state.approvalBusy = null;
  if (invalidate) ++state.scopeSerial;
  boardAvailable = false;
  boardEnvironment = null;
  boardEnvironmentError = "";
  boardEnvironmentReadAt = "";
  setupHealth = null;
  if (invalidate || state.pauseError) requests.invalidate(path => path === "/api/health");
  ++setupHealthSerial;
  boardJournalOpen = false;
  setupActionError = "";
  ++boardEnvironmentSerial;
  $("board-setup").dataset.signature = "";
  $("board-setup").hidden = true;
  setChildren($("board-setup"));
  reviewAvailable = false;
  setChildren($("board-vision"));
  setChildren($("project-vision"));
  renderReviewCount(0);
  closeTask();
  boardWrites.close();
  resetActivity(state.pauseError ? emptyBox("Events are unavailable.", false, "failed") : state.activityTail ? emptyBox("Loading events…", false, "loading") : undefined,
    state.pauseError ? emptyBox("Dispatch errors are unavailable.", false, "failed") : state.activityTail ? emptyBox("Loading dispatch errors…", false, "loading") : undefined);
  $("activity-kinds").replaceChildren();
  flow.view = null;
  flow.loaded = false;
  flow.runs = [];
  flow.tasks = [];
  flow.selected = null;
  flow.wikiStates = null;
  pipelineRunRows = null;
  renderTopology(pipelineFlowmap(routingRoutes, Date.now()), flow.bindings);
  setChildren($("pipeline-runs"));
  renderBoard();
  renderReview();
}

function selectProject(id) {
  if (id === state.project) return;
  closeReader();
  if (state.tab === "review") state.readerAuto = true;
  state.project = id;
  try { localStorage.setItem("mc.project", id); } catch {}
  resetProjectView();
  renderProjectContext();
  renderHeader();
  tick();
}

/** The lease state text of one Project, from its own lease rows. */
function projectLeaseText(id, now) {
  const rows = (state.leases ?? []).filter((row) => row.project === id);
  return leaseStateText(leaseBadge(rows, now, state.settings));
}

/** One row of the Project list: the selection button and the pause
 * control of that Project. */
function projectRowNode(row, leaseText) {
  const paused = row.status === "paused";
  const archived = row.status === "archived";
  const selected = row.id === state.project;
  const rowClass = ["ws-row", selected ? "selected" : "", archived ? "archived" : paused ? "paused" : ""].join(" ").trim();
  const setup = row.setup_status && row.setup_status !== "ready";
  const action = setup ? el("button", { type: "button", class: "ws-act", text: "Vision", onclick: () => { closeProjectPanel(false); openPage("vision", [row.id]); } }) : archived ? null : el("button", {
    type: "button", class: "ws-act", text: paused ? "Resume" : "Pause",
    "aria-label": `${paused ? "Resume" : "Pause"} ${projectName(row.id, state.pause?.projects)}`,
    disabled: state.pauseBusy || !state.pause, data: { project: row.id },
    onclick: () => changePipelinePause(`/api/projects/${encodeURIComponent(row.id)}/${paused ? "resume" : "pause"}`),
  });
  const wikiNode = viewerLinkNode(projectWikiLinkView(row.id, projectName(row.id, state.pause?.projects), projectWikiLink(row.id)));
  return el("li", { class: rowClass, testid: `project-row-${row.id}` },
    svg("svg", { class: "option-check", viewBox: "0 0 18 18", "aria-hidden": "true" },
      svg("path", { d: "M3.5 9.5 7.2 13 14.5 5", fill: "none", stroke: "currentColor", "stroke-width": "2", "stroke-linecap": "round", "stroke-linejoin": "round" })),
    el("button", {
      type: "button", class: "option", testid: `project-${row.id}`, title: projectName(row.id, state.pause?.projects),
      "aria-current": selected ? "true" : "false", data: { project: row.id },
      onclick: () => { selectProject(row.id); closeProjectPanel(true); },
    },
      el("span", { class: "option-name", text: projectName(row.id, state.pause?.projects), title: projectName(row.id, state.pause?.projects) }),
      el("span", { class: "option-meta" },
        el("span", { class: archived ? "state archived" : paused ? "state paused" : "state", text: projectStateText(row.status, row.setup_status) }),
        el("span", { class: "separator", "aria-hidden": "true", text: "·" }),
        el("span", { text: leaseText }),
      ),
    ),
    wikiNode,
    action,
  );
}

/** A refresh while the list is open rebuilds the rows only when the
 * signature changes. The rebuild returns focus to the row with the same id. */
function renderProjectContext() {
  const context = $("project-context");
  const rows = state.pause?.projects ?? [];
  context.hidden = false;
  const scoped = PROJECT_PAGES.has(state.tab);
  context.classList.toggle("muted", !scoped);
  renderVisionNotice();
  const selected = rows.find((row) => row.id === state.project);
  const listbox = $("project-listbox");
  const name = !state.pause ? (state.pauseError ? "Project state unavailable" : $("project-name").textContent || "Project")
    : selected ? projectName(selected.id, rows) : "No Projects";
  if (selected) {
    try { localStorage.setItem("mc.project-label", JSON.stringify({ id: selected.id, name })); } catch {}
  }
  $("project-name").textContent = name;
  const button = $("project-button");
  button.disabled = !scoped || !state.projectReady;
  button.title = !scoped ? "This page applies to all Projects." : !state.projectReady ? (state.pauseError || "Please wait for Project data.") : rows.length === 1 ? "Project controls" : "Select a Project";
  const pauseText = selectedPauseText();
  $("project-pause-dot").hidden = !pauseText;
  $("board-paused").hidden = !pauseText;
  button.setAttribute("aria-label", `${name}${pauseText ? `, ${pauseText.toLowerCase()}` : ""}. ${!scoped ? "This page applies to all Projects." : rows.length === 1 ? "Open Project controls." : "Open the Project list."}`);
  $("project-count").textContent = state.projectReady ? String(rows.length) : "—";
  if (context.hidden || !scoped) {
    // The active page tab takes focus when the Project button is unavailable.
    const focused = context.contains(document.activeElement) || $("project-panel").contains(document.activeElement);
    closeProjectPanel(false);
    if (focused) document.querySelector(`nav.seg .tab[data-page="${state.tab}"]`)?.focus();
  }
  const now = Date.now();
  const leaseTexts = rows.map((row) => projectLeaseText(row.id, now));
  const wikiLinks = rows.map((row) => projectWikiLink(row.id));
  const signature = JSON.stringify([rows, Boolean(state.pause), state.project, state.projectReady, leaseTexts, wikiLinks]);
  renderViewerLinks();
  if (listbox.dataset.state !== signature) {
    const active = listbox.contains(document.activeElement) ? document.activeElement : null;
    const focused = active ? { id: active.dataset.project, action: active.classList.contains("ws-act"), wiki: active.classList.contains("ws-wiki") } : null;
    const empty = !state.pause ? "Project state unavailable." : !rows.length ? "No Projects registered." : "";
    setChildren(listbox, empty ? el("li", { class: "scope-empty", text: empty }) : rows.map((row, index) => projectRowNode(row, leaseTexts[index])));
    listbox.dataset.state = signature;
    if (focused) {
      ((focused.action ? projectAction(focused.id) : null) ?? (focused.wiki ? projectWiki(focused.id) : null) ??
        projectOption(focused.id) ?? projectOptions()[0] ?? $("pipeline-pause")).focus({ preventScroll: true });
    }
  }
  for (const action of listbox.querySelectorAll(".ws-act")) action.disabled = state.pauseBusy || !state.pause;
}

function projectOptions() {
  return [...$("project-listbox").querySelectorAll(".option")];
}

function projectOption(id) {
  return projectOptions().find((option) => option.dataset.project === id) ?? null;
}

function projectAction(id) {
  return [...$("project-listbox").querySelectorAll(".ws-act")].find((action) => action.dataset.project === id) ?? null;
}

function projectWiki(id) {
  return [...$("project-listbox").querySelectorAll(".ws-wiki")].find((link) => link.dataset.project === id) ?? null;
}

// -------------------------------------------------------------- viewer links

/** True until a status response supplies the viewer facts. */
function viewersPending() {
  return systemView.pending && !systemView.viewers;
}

function projectWikiLink(id) {
  return wikiLink(systemView.viewers, id, state.pause ? state.pause.projects : null,
    { pending: viewersPending(), registryError: state.pauseError });
}

/** The Traces link follows the status response. */
function renderViewerLinks() {
  applyViewerLink($("header-traces"), tracesLink(systemView.viewers, viewersPending()));
}

let viewersPaint = "";
/** A change of the viewer facts repaints each link that depends on them. */
function renderViewers() {
  const key = JSON.stringify([systemView.viewers, viewersPending()]);
  if (key === viewersPaint) return;
  viewersPaint = key;
  renderProjectContext();
  if (pipelineRunRows !== null) {
    renderRuns(pipelineRunRows);
    paintFlowmap();
  }
  renderSetup();
}

function openProjectPanel() {
  if ($("project-context").hidden || $("project-button").disabled) return;
  renderProjectContext();
  $("project-panel").hidden = false;
  $("project-button").setAttribute("aria-expanded", "true");
  (projectOption(state.project) ?? projectOptions()[0] ?? $("pipeline-pause")).focus();
}

function closeProjectPanel(returnFocus) {
  const panel = $("project-panel");
  if (panel.hidden) return;
  panel.hidden = true;
  $("project-button").setAttribute("aria-expanded", "false");
  if (returnFocus) $("project-button").focus();
}

function moveProjectFocus(step) {
  const options = projectOptions();
  if (!options.length) return;
  const id = document.activeElement?.dataset?.project;
  const current = options.findIndex((option) => option.dataset.project === id);
  options[(current + step + options.length) % options.length].focus();
}

$("project-button").addEventListener("click", () => {
  if ($("project-panel").hidden) openProjectPanel();
  else closeProjectPanel(true);
});
$("project-button").addEventListener("keydown", (event) => {
  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
    event.preventDefault();
    openProjectPanel();
  }
});
// A wiki link opens a new tab. The list closes like after each other action.
$("project-listbox").addEventListener("click", (event) => {
  if (event.target instanceof Element && event.target.closest(".ws-wiki[href]")) closeProjectPanel(true);
});
$("project-listbox").addEventListener("keydown", (event) => {
  const options = projectOptions();
  switch (event.key) {
    case "ArrowDown": event.preventDefault(); moveProjectFocus(1); break;
    case "ArrowUp": event.preventDefault(); moveProjectFocus(-1); break;
    case "Home": event.preventDefault(); options[0]?.focus(); break;
    case "End": event.preventDefault(); options[options.length - 1]?.focus(); break;
  }
});
document.addEventListener("keydown", (event) => {
  if (event.key === "Escape" && !$("project-panel").hidden) {
    event.preventDefault();
    closeProjectPanel(true);
  }
});
// A press on the panel outside a button keeps focus on the focused button.
$("project-panel").addEventListener("pointerdown", (event) => {
  if (!(event.target instanceof Element && event.target.closest("button, a"))) event.preventDefault();
});
// Focus that leaves the switcher closes the list.
$("project-panel").addEventListener("focusout", (event) => {
  if (event.relatedTarget instanceof Element && !event.relatedTarget.closest(".scope-switcher")) closeProjectPanel(false);
});
document.addEventListener("pointerdown", (event) => {
  if ($("project-panel").hidden) return;
  if (event.target instanceof Element && event.target.closest(".scope-switcher")) return;
  closeProjectPanel(false);
});

function projectPauseNode(row) {
  const paused = row.status === "paused";
  const archived = row.status === "archived";
  return el("div", { class: "project-pause-control", testid: `pause-project-${row.id}` },
    el("span", { text: projectStateText(row.status, row.setup_status), role: "status" }),
    archived || (row.setup_status && row.setup_status !== "ready") ? null : el("button", {
      type: "button", class: "btn small", text: paused ? "Resume Project" : "Pause Project",
      "aria-label": `${paused ? "Resume" : "Pause"} ${projectName(row.id, state.pause?.projects)}`,
      disabled: state.pauseBusy || !state.pause,
      onclick: () => changePipelinePause(`/api/projects/${encodeURIComponent(row.id)}/${paused ? "resume" : "pause"}`),
    }),
  );
}

function renderPipelinePause() {
  const pause = state.pause;
  const all = pauseAllView(pause?.projects);
  $("pipeline-controls").hidden = !all.available && !state.pauseError;
  const button = $("pipeline-pause");
  button.hidden = !all.available;
  button.textContent = all.allPaused ? "Resume all" : "Pause all";
  button.classList.toggle("rust", all.allPaused);
  button.title = all.allPaused ? "Resume every paused Project." : "Pause every Project.";
  button.disabled = state.pauseBusy || !pause || !all.available;
  $("pipeline-pause-error").hidden = !state.pauseError;
  $("pipeline-pause-error").textContent = state.pauseError;
  renderProjectContext();
  renderHeader();
  if (state.projectReady) renderReviewCount(queueStats(ownRows(state.packets), state.settings).awaiting);
  if (state.tab === "configs") renderConfigs();
}

let pauseActive = null;
function refreshPipelinePause(force = false) {
  if (pauseActive) return pauseActive;
  pauseActive = readPipelinePause(force).finally(() => { pauseActive = null; });
  return pauseActive;
}
async function readPipelinePause(force = false) {
  if (!force && (state.pauseBusy || Date.now() - state.pauseReadAt < LEASE_MS)) return;
  const serial = ++state.pauseSerial;
  try {
    const pause = await api("/api/pause");
    if (serial !== state.pauseSerial) return;
    const previous = state.project;
    const wasReady = state.projectReady;
    state.pause = pause;
    state.projectReady = true;
    state.pauseError = "";
    if (!pause.projects.some((row) => row.id === state.project)) {
      state.project = (pause.projects.find((row) => row.status !== "archived") ?? pause.projects[0])?.id ?? null;
      try {
        if (state.project) localStorage.setItem("mc.project", state.project);
        else localStorage.removeItem("mc.project");
      } catch {}
    }
    if (previous !== state.project || !wasReady) resetProjectView(wasReady);
    projectList = projectList.map((row) => ({
      ...row, status: pause.projects.find((ws) => ws.id === row.id)?.status ?? row.status,
    }));
    state.pauseReadAt = Date.now();
    if (wasReady && previous !== state.project) void tick();
  } catch (err) {
    if (serial !== state.pauseSerial) return;
    state.pause = null;
    state.projectReady = false;
    state.pauseError = err.message;
    resetProjectView(false);
  }
  renderPipelinePause();
  renderBoard();
}

async function changePipelinePause(path) {
  if (state.pauseBusy) return;
  state.pauseBusy = true;
  ++state.pauseSerial;
  state.pauseError = "";
  renderPipelinePause();
  try {
    await api(path, { method: "POST" });
    await refreshPipelinePause(true);
  } catch (err) {
    state.pauseError = err.message;
  } finally {
    state.pauseBusy = false;
    renderPipelinePause();
  }
}

$("pipeline-pause").addEventListener("click", () => changePipelinePause(pauseAllView(state.pause?.projects).allPaused ? "/api/resume" : "/api/pause"));

/** The Review tab's own badge: a decision owed is the one thing on this
 * dashboard that waits on the operator personally. */
function renderReviewCount(count) {
  const badge = $("review-count") ?? document.querySelector('[data-testid="review-count"]');
  if (!badge) return;
  badge.textContent = count > 0 ? String(count) : "";
  badge.hidden = count === 0;
}

// ---------------------------------------------------------------- 1. Board

let boardAvailable = false;
let reviewAvailable = false;
let boardTasks = [];
let boardProjects = [];
let boardPackets = [];
/** Every run row, read only while the dialog is open: its run history is
 * the runs whose subject is the task. */
let boardRuns = [];

const boardWrites = createBoardWrites({
  dialog: $("board-write-dialog"), api,
  getProjects: () => boardProjects,
  selectedProject: () => state.projectReady ? state.project : null,
  onCollie: () => { closeTask(); openPage("collie"); },
  onSaved: async (result, submitted) => {
    const scope = state.scopeSerial;
    const shown = state.boardOpen;
    if (submitted.project !== state.project) return;
    if (state.tab === "review" && shown !== null) {
      await refreshReview();
      if (scope === state.scopeSerial && submitted.action === "cancel") closeTask();
      return;
    }
    if (state.tab !== "board") return;
    await refreshBoard();
    if (scope !== state.scopeSerial || submitted.action === "cancel") return;
    // A task dialog that closes during the refresh stays closed.
    if (shown !== null && state.boardOpen !== shown) return;
    if (boardTasks.some((row) => row.id === result.task_id)) openTask(result.task_id);
  },
});

$("board-create").addEventListener("click", () => boardWrites.openCreate());

function affectedRun(row) {
  return state.leases.find((lease) => lease.run_id &&
    lease.subject === row.id) ?? null;
}

// Each card shows the task, its stage age, and its current status.
function cardNode(card) {
  const open = state.boardOpen === card.id;
  const classes = ["card"];
  if (card.active) classes.push("active", card.stale ? "stale" : "running");
  else if (card.attention) classes.push("attention");
  if (open) classes.push("open");
  const chips = [
    (card.active || card.stopPending) &&
      el(
        "span",
        { class: `chip ${card.stopPending ? "tone-stone" : card.stale ? "stale" : "running"}`, testid: "board-card-running" },
        !card.stopPending && el("i", { class: "dot" }),
        document.createTextNode(card.running),
      ),
    card.projectInfrastructure && el("span", { class: "chip attention", text: "Infrastructure failure", title: card.projectInfrastructure.condition }),
    card.blocked && el("span", { class: "chip attention", title: card.reason, text: card.proposalDecisionID ? "Awaits shared proposal decision" : "Decision Pending" }),
    card.stalled && el("span", { class: "chip attention", text: "Retries Exhausted", title: "Three task run failures used the task run failure allowance. Automatic retries stopped." }),
    card.connectionWait && el("span", {
      class: "chip attention", testid: "board-card-wait",
      text: `Waits for ${connectionName(card.connectionWait, systemView.snapshot?.connections) ?? "a connection"}`,
      title: "A connection check stopped the next run. Culler starts the run when the connection is available.",
    }),
    card.approved
      ? el("span", { class: "tag scope", testid: "board-card-approved", text: "Approved" })
      : card.packet !== null && tag("Review packet available", "scope review-packet"),
    ...card.tags.map((t) => tag(t.label, t.kind)),
  ].filter(Boolean);
  return el(
    "div",
    { class: classes.join(" "), testid: "board-card", data: { task: String(card.id) } },
    el(
      "button",
      {
        type: "button",
        class: "head",
        "aria-haspopup": "dialog",
        testid: "board-card-open",
        onclick: () => openTask(card.id),
      },
      el("span", { class: "card-meta" },
        el("span", { class: "card-id", text: `#${card.id}` }),
        card.stageAge && el("span", {
          class: "card-age",
          text: card.stageAge === "0m" ? "Just entered" : `${card.stageAge} in stage`,
          title: "Time in the current stage",
        }),
      ),
      el("div", { class: "title", title: card.title, text: card.title }),
      chips.length > 0 && el("div", { class: "metaline" }, chips),
    ),
  );
}

/** Opens the task dialog of one task. `initialRow` or the list row paints at
 * once; the full row and the run history land after their read. */
function openTask(id, initialRow = null) {
  state.boardOpen = id;
  state.boardDetail = initialRow ? { id, row: initialRow, error: null } : null;
  renderBoard();
  const dialog = $("task-dialog");
  if (!dialog.open) showDialog(dialog);
  readBoardDetail().then(renderBoard, showError);
}

function closeTask() {
  const dialog = $("task-dialog");
  if (dialog.open) dialog.close();
}

// The dialog closes by its own button, by Esc (the dialog's cancel), or by a
// click on the backdrop; every route ends in the close event, so the state
// follows the element rather than the other way round.
$("task-dialog").addEventListener("close", () => {
  state.boardOpen = null;
  state.boardDetail = null;
  boardRuns = [];
  renderBoard();
});
$("task-dialog").addEventListener("click", (event) => {
  if (event.target === event.currentTarget) closeTask();
});

/** Reads the open task's full row and the run history behind it. The list
 * row stands in until the get lands, so the dialog opens at once. */
async function readBoardDetail() {
  const id = state.boardOpen;
  if (id === null) {
    boardRuns = [];
    return;
  }
  // The run history decorates the dialog; a failed read keeps the last one
  // rather than blocking the board's paint. The artifact roots supply the host
  // path of the Verifier evidence.
  const [runs, read] = await Promise.all([
    api("/api/runs").catch(() => ({ runs: boardRuns })),
    api(`/api/tasks/${id}`).then(
      (row) => ({ id, row, error: null }),
      (err) => ({ id, row: null, error: err instanceof Error ? err.message : String(err) }),
    ),
    readArtifactRoots().catch(() => null),
  ]);
  // A dialog closed, or moved to another task, during the read keeps nothing
  // from it.
  if (state.boardOpen !== id) return;
  boardRuns = runs.runs ?? [];
  state.boardDetail = read;
  if (read.row) boardWrites.updateRecord(read.row);
}

/** The task dialog (taskdialog.js), fed from the board's state: the full row
 * once its read lands, the list row until then, and the task's runs newest
 * first. Its two containers are refilled in place, so the body's scroll
 * position and the focused button survive the poll. */
function renderTaskDialog() {
  const id = state.boardOpen;
  const dialog = $("task-dialog");
  if (id === null) {
    setChildren($("task-dialog-head"));
    setChildren($("task-dialog-body"));
    return;
  }
  const now = Date.now();
  const read = state.boardDetail;
  const row = read?.row ?? boardTasks.find((t) => t.id === id);
  if (!row) {
    closeTask();
    return;
  }
  const d = taskDetail(row, { tasks: boardTasks, leases: state.leases, nowMs: now, settings: state.settings, packets: state.packets, projects: state.pause?.projects });
  const packet = readerRow(row.id);
  const runs = runsView(
    row.run_history ?? taskRuns(boardRuns,row),
    now,
    boardRuns.length,
  );
  const focused = document.activeElement?.closest?.("#task-dialog [data-testid]")?.dataset.testid ?? null;
  const disclosures = new Set([...dialog.querySelectorAll("details[open][data-disclosure]")].map((node) => node.dataset.disclosure));
  const reports = new Map([...dialog.querySelectorAll("[data-report]")].map((node) => [node.dataset.report, node]));
  const sharedDecision = row.proposal_decision_id && systemView.snapshot?.conditions?.find(condition =>
    condition.proposal_decision_id === row.proposal_decision_id && condition.project_id === row.project && !condition.task_id);
  const controls = read?.row ? boardRecordControls(row, {
    onAction: (action) => boardWrites.openAction(action, row, affectedRun(row)),
  }) : null;
  if (controls && sharedDecision) controls.append(el("button", {
    type: "button", class: "btn", testid: "task-shared-proposal-decision", text: "View shared proposal decision",
    onclick: () => { closeTask(); void systemDetails(sharedDecision); },
  }));
  setChildren($("task-dialog-head"), taskDialogHead(d, { onClose: closeTask }));
  setChildren(
    $("task-dialog-body"),
    taskDialogBody(d, {
      loading: read === null,
      error: read?.error ?? null,
      runs,
      discussion: (d.blocked || d.stalled || d.stale || d.connectionWait || d.projectInfrastructure)
        ? diagnosticButton(taskDiagnostic(row, d), askDiagnostic, "task-ask-collie") : null,
      controls,
      history: read?.row ? boardHistoryNode(row, apiBytes) : null,
      onRun: openRunDiagnostics,
      readReport: apiBytes,
      viewers: systemView.viewers,
      viewersPending: viewersPending(),
      connections: systemView.snapshot?.connections ?? [],
      // On the Review page the dialog sits over the reader of its own packet,
      // so the link has no destination. From another page, the reader opens
      // only a packet from the packet list it has read, so the read lands
      // first, in the live view where a packaged task's packet is. The reader
      // entry sits over the list entry of the Review page, and the page does
      // not open its first packet.
      onPacket: state.tab === "review" ? null : async () => {
        closeTask();
        openPage("review");
        state.readerAuto = false;
        setPacketView("live");
        try {
          await refreshReview();
        } catch (err) {
          showError(err, { read: true });
          return;
        }
        if (state.tab === "review") selectPacket(d.packet);
      },
    }),
  );
  for (const node of dialog.querySelectorAll("details[data-disclosure]")) node.open = disclosures.has(node.dataset.disclosure);
  for (const node of dialog.querySelectorAll("[data-report]")) {
    const previous = reports.get(node.dataset.report);
    if (previous && previous !== node) node.replaceWith(previous);
  }
  if (focused !== null) dialog.querySelector(`[data-testid="${focused}"]`)?.focus({ preventScroll: true });
}

function columnNode(column, pending = false, failure = "") {
  const emptyText = {
    proposed: "New proposals appear here.",
    seeded: "Tasks await execution here.",
    worked: "Tasks await verification here.",
    verified: "Tasks await a review packet here.",
    packaged: "Tasks with review packets appear here.",
  };
  return el(
    "div",
    { class: "col", testid: `board-column-${column.status}`, data: { stage: column.status } },
    el(
      "div",
      { class: "column-head" },
      el("h3", {},
        el("i", { class: `dot dot-${column.status}`, "aria-hidden": "true" }),
        document.createTextNode(column.label),
      ),
      el("span", {
        class: "column-count",
        text: pending ? "—" : column.count,
        "aria-label": pending ? "Task count is unavailable" : `${column.count} ${column.count === 1 ? "task" : "tasks"}`,
      }),
    ),
    pending ? el("div", { class: "stack" }, failure ? emptyBox(failure, false, "failed") : skeletonCards())
      : column.cards.length === 0
      ? el("div", { class: "empty board-empty" },
        el("span", { class: "board-empty-title", text: "No tasks here" }),
        el("span", { class: "board-empty-copy", text: emptyText[column.status] }),
      )
      : el("div", { class: "stack" }, column.cards.map(cardNode)),
  );
}

function projectActionNode(title, description, action, onAction, details = null, testid = null) {
  return el("section", { class: "project-action", testid, "aria-label": title },
    el("div", { class: "project-action-copy" },
      el("p", { class: "project-action-label", text: action ? "Project action" : "Project status" }),
      el("h3", { text: title }),
      el("p", { class: "project-action-description", text: description }),
      details,
    ),
    action ? el("button", { class: "btn primary", type: "button", text: action, onclick: onAction }) : null,
  );
}

function visionNoticeNode(notices, prefix) {
  if (notices.length === 0) return null;
  return el("div", { class: "project-actions", testid: `${prefix}-vision-notice` },
    ...notices.map((row) => projectActionNode(
      `${row.title} needs a Project Vision`,
      "Describe the Project goal so Culler can plan work.",
      "Open Vision",
      () => projectControls.open(row.id),
      el("details", { class: "project-action-details" },
        el("summary", { text: "Why Vision is required" }),
        el("p", { text: row.reason }),
        el("p", {}, document.createTextNode("Required file: "), el("code", { text: row.path })),
      ),
      `${prefix}-vision-${row.id}`,
    )),
  );
}

function renderVisionNotice() {
  if (!state.projectReady) {
    setChildren($("board-vision"));
    setChildren($("project-vision"));
    $("project-vision").hidden = true;
    return;
  }
  const notices = visionNotices(boardProjects.filter(row => !row.setup_status || row.setup_status === "ready"));
  setChildren($("board-vision"), visionNoticeNode(notices, "board"));
  const selected = notices.filter((row) => state.projectReady && row.id === state.project);
  const host = $("project-vision");
  host.hidden = state.tab === "board" || !PROJECT_PAGES.has(state.tab) || selected.length === 0;
  setChildren(host, host.hidden ? null : visionNoticeNode(selected, "project"));
}

async function refreshVisionNotice() {
  if (state.tab === "board" || !PROJECT_PAGES.has(state.tab)) return;
  const scope = state.scopeSerial;
  const [projects] = await Promise.all([api("/api/projects"), refreshPipelinePause()]);
  if (scope !== state.scopeSerial) return;
  boardProjects = projects.projects ?? [];
  renderVisionNotice();
}

let projectReadiness = null;
let projectReadError = "";
let projectReadAvailable = false;
let boardEnvironment = null;
let boardEnvironmentError = "";
let boardEnvironmentReadAt = "";
let boardEnvironmentSerial = 0;
let setupHealth = null;
let setupHealthSerial = 0;
let boardJournalOpen = false;
const setupActionProjects = new Set();
let setupActionError = "";

function showSetupJournal() {
  if (boardJournalOpen) return true;
  const env = boardEnvironment?.environment;
  const project = state.pause?.projects.find(row => row.id === state.project);
  if (!env && project?.setup_status === "configured") return true;
  return Boolean(env?.setup_id && !initialSetupComplete(boardEnvironment) && env.setup_status !== "ready" && !env.selected_revision_id);
}
function setupSurfaceVisible() {
  const project = state.pause?.projects.find(row => row.id === state.project);
  return showSetupJournal() || Boolean(boardEnvironmentError && project?.setup_status && project.setup_status !== "ready");
}
function updateBoardCommandBar() {
  commandBar.setVisible(state.tab === "board" && !setupSurfaceVisible());
}
async function setupAction(action, body) {
  const project = state.project, scope = state.scopeSerial;
  if (setupActionProjects.has(project)) return;
  setupActionProjects.add(project);
  setupActionError = "";
  ++boardEnvironmentSerial;
  renderSetup();
  try {
    const result = await api(`/api/environment/${action}`, { method: "POST", body: JSON.stringify({ project_id: project, ...body }) });
    if (result.environment?.project_id !== project) throw new Error("The response identifies another Project.");
    if (project !== state.project) return;
    await Promise.all([refreshBoardEnvironment(state.scopeSerial), refreshPipelinePause(true)]);
  } catch (error) {
    if (scope === state.scopeSerial && project === state.project) setupActionError = error.message;
  } finally {
    setupActionProjects.delete(project);
    if (project === state.project) { renderBoard(); renderHeader(); }
  }
}
function renderSetup() {
  const host = $("board-setup");
  const view = boardEnvironment?.environment?.project_id === state.project ? boardEnvironment : null;
  if (view?.environment?.setup_id && !initialSetupComplete(view) && !view.environment.selected_revision_id && view.environment.setup_status !== "ready") boardJournalOpen = true;
  host.hidden = !showSetupJournal() && !boardEnvironmentError;
  if (host.hidden) { setChildren(host); host.dataset.signature = ""; return; }
  if (!setupSurfaceVisible()) {
    setChildren(host, el("div", { class: "request-status", role: "status" },
      el("span", { text: `Setup history is unavailable. ${boardEnvironmentError}` }),
      el("button", { type: "button", class: "btn small", text: "Retry", onclick: () => tick() })));
    host.dataset.signature = "";
    return;
  }
  const model = setupJournalModel(view, { project: state.pause?.projects.find(row => row.id === state.project),
    unavailable: Boolean(boardEnvironmentError), lastReadAt: boardEnvironmentReadAt, health: setupHealth });
  const busy = setupActionProjects.has(state.project);
  const signature = JSON.stringify([view, setupHealth, boardEnvironmentError, boardEnvironmentError && boardEnvironmentReadAt, busy, setupActionError,
    systemView.viewers, viewersPending()]);
  if (host.dataset.signature === signature) return;
  const expanded = new Map([...host.querySelectorAll("details[data-journal-key]")].map(node => [node.dataset.journalKey, node.open]));
  const active = host.contains(document.activeElement) ? document.activeElement : null;
  const focusedKey = active?.closest("[data-journal-key]")?.dataset.journalKey;
  const focusedID = active?.dataset.testid;
  const operationKey = node => {
    const data = node?.closest("[data-operation]")?.dataset;
    return data?.operation;
  };
  const focusedOperation = operationKey(active);
  const focusedLabel = active?.getAttribute("aria-label");
  const focusedText = active?.textContent;
  const focusedTag = active?.tagName;
  const scroll = { x: window.scrollX, y: window.scrollY };
  setChildren(host, setupJournalNode(model, {
    approve: revisionID => setupAction("approve", { revision_id: revisionID }),
    deny: revisionID => setupAction("deny", { revision_id: revisionID }),
    retry: () => setupAction("retry", {}),
    environment: () => openPage("environment", [state.project]),
    editDockerfile: () => {
      openPage("environment", [state.project]);
      void environmentRead?.then(() => environmentPage.focusDockerfile());
    },
    openBoard: () => { boardJournalOpen = false; renderBoard(); renderHeader(); $("board-heading").querySelector("h2").focus({ preventScroll: true }); },
    retryRead: () => tick(), busy, actionError: setupActionError,
    askDiagnostic,
  }, { viewers: systemView.viewers, pending: viewersPending() }));
  host.dataset.signature = signature;
  for (const node of host.querySelectorAll("details[data-journal-key]")) {
    if (expanded.has(node.dataset.journalKey)) node.open = expanded.get(node.dataset.journalKey);
  }
  if (active) {
    // A file viewer returns its own nodes to the journal.
    const target = host.contains(active) ? active
      : focusedID ? [...host.querySelectorAll("[data-testid]")].find(node => node.dataset.testid === focusedID && operationKey(node) === focusedOperation)
        : focusedTag === "SUMMARY" ? [...host.querySelectorAll("[data-journal-key]")].find(node => node.dataset.journalKey === focusedKey)?.querySelector("summary")
          : [...host.querySelectorAll("button, a, [tabindex]")].find(node => node.tagName === focusedTag
            && node.getAttribute("aria-label") === focusedLabel && node.textContent === focusedText
            && operationKey(node) === focusedOperation);
    (target ?? host.querySelector("h1"))?.focus({ preventScroll: true });
  }
  if (expanded.size) window.scrollTo(scroll.x, scroll.y);
}
/** Paints the Board page and the open task dialog. The dialog also opens on
 * other pages, so its paint needs no Board read. */
function renderBoard() {
  paintBoardPage();
  renderTaskDialog();
}

function paintBoardPage() {
  renderSetup();
  commandBar.setContext(state.pause?.projects.find(project => project.id === state.project) ?? (state.project ? { id: state.project } : null), state.projectReady);
  const empty = emptyProjectRegistry();
  const unknown = !state.projectReady;
  const journal = setupSurfaceVisible();
  updateBoardCommandBar();
  $("board-pending").hidden = !unknown || Boolean(state.pauseError);
  const loading = !state.pauseError && !projectReadError && !state.dockerUnavailable && (unknown || !boardAvailable && !empty);
  $("board-pane").setAttribute("aria-busy", String(loading));
  for (const id of ["board-heading", "board-columns", "board-stats", "board-create"]) $(id).hidden = empty || Boolean(state.pauseError);
  if (journal) {
    for (const id of ["board-heading", "board-columns", "board-stats", "board-create", "board-project", "board-vision"]) $(id).hidden = true;
    setChildren($("board-project"));
    setChildren($("board-vision"));
    $("board-pane").setAttribute("aria-busy", "false");
    return;
  }
  $("board-vision").hidden = false;
  if (unknown || !boardAvailable && !empty) {
    const failure = projectReadError || state.pauseError;
    $("board-project").hidden = !failure;
    setChildren($("board-project"), failure && el("p", { role: "status", text: failure }),
      failure && el("button", { type: "button", class: "btn", text: "Retry", onclick: () => tick() }));
    $("board-create").disabled = true;
    setChildren($("board-stats"), ...boardStatItems({ blocked: "—", activeRuns: "—", total: "—", stalled: "—" })
      .map(item => stat(item.label, item.value, { ...item, zero: false, alert: false })));
    setChildren($("board-columns"), ...boardColumns([], [], {}).map(column => columnNode(column, true,
      state.dockerUnavailable ? "The board will load when Docker starts." : failure ? "Task data is unavailable." : "")));
    return;
  }
  const project = empty ? "empty" : projectState({ available: projectReadAvailable && !projectReadError, projects: boardProjects, selected: state.project });
  const host = $("board-project");
  host.hidden = false;
  if (empty) {
    setChildren(host,
      el("div", { class: "project-welcome" },
        $("project-welcome-logo").content.cloneNode(true),
        el("p", { class: "project-eyebrow", text: "Board" }),
        el("h2", { text: "Add your first Project" }),
        el("p", { text: "Import an existing Project or create a new Project from scratch." }),
        el("button", { type: "button", class: "btn primary", text: "Add Project", testid: "board-add-project", onclick: () => projectControls.open() }),
        el("div", { class: "project-explanation" },
          el("p", { text: "Culler develops each Project from its files and vision." }),
          systemExplanation(),
        ),
        projectReadiness && !projectReadiness.ready ? agentReadinessNode(() => agentProfiles.open()) : null,
      ),
    );
    setChildren($("board-vision"));
    renderReviewCount(0);
    return;
  } else if (project === "unavailable" || project === "empty") {
    setChildren(host, el("p", { role: "status", text: projectReadError || "Read the Project state before project setup." }),
      el("button", { type: "button", class: "btn", text: "Retry", onclick: () => tick() }));
  } else {
    const setup = boardProjects.find(row => row.id === state.project)?.setup_status;
    setChildren(host,
      !boardEnvironment?.environment?.setup_id && (setup === "configured" || setup === "vision") && project !== "archived" ? projectActionNode(
        "Initialization is incomplete",
        setup === "configured" ? "Open Vision and select Initialize project to prepare your Project." : "Save the Project goal on the Vision page before you initialize the Project.",
        "Open Vision", () => projectControls.open(state.project),
      ) : setup === "ready" && project === "paused" ? projectActionNode(
        "This Project is paused",
        "Your Project setup remains saved.",
        null, null,
      ) : null,
    );
    host.hidden = !host.childElementCount;
  }
  $("board-create").disabled = !state.projectReady || !boardProjects.length;
  if (state.dockerUnavailable) {
    setChildren($("board-columns"), emptyBox("The board will load when Docker starts.", false, "failed"));
    return;
  }
  renderVisionNotice();
  const now = Date.now();
  const stats = boardStats(ownRows(boardTasks), ownRows(state.leases));
  // One active card per Project at most, so the active stat lists every one.
  setChildren($("board-stats"), ...boardStatItems(stats).map(item => stat(item.label, item.value, item)));
  const columns = boardColumns(ownRows(boardTasks), ownRows(state.leases), {
    nowMs: now,
    settings: state.settings,
    packets: ownRows(boardPackets),
    projects: state.pause?.projects,
  });
  // Retain focus when the task remains available.
  const focused = document.activeElement?.closest?.('[data-testid="board-card"]')?.dataset.task ?? null;
  setChildren($("board-columns"), ...columns.map(column => columnNode(column)));
  if (focused !== null) {
    $("board-columns").querySelector(`[data-task="${focused}"] > .head`)?.focus({ preventScroll: true });
  }
}

async function refreshBoardEnvironment(scope) {
  const project = state.project, serial = ++boardEnvironmentSerial;
  try {
    const environment = await api("/api/environment/get", { method: "POST", body: JSON.stringify({ project_id: project }) });
    if (scope !== state.scopeSerial || serial !== boardEnvironmentSerial) return;
    if (environment.environment?.project_id !== project) throw new Error("The response identifies another Project.");
    boardEnvironment = environment;
    boardEnvironmentError = "";
    boardEnvironmentReadAt = new Date().toISOString();
  } catch (error) {
    if (scope !== state.scopeSerial || serial !== boardEnvironmentSerial) return;
    boardEnvironmentError = error.message;
  }
  renderBoard(); renderHeader();
}

async function refreshSetupHealth(scope) {
  const project = state.project, serial = ++setupHealthSerial;
  let response;
  try { response = await api("/api/health", { maxAge: 0 }); } catch {}
  if (scope !== state.scopeSerial || project !== state.project || serial !== setupHealthSerial) return;
  setupHealth = acceptSetupHealth(setupHealth, response);
  renderSetup(); renderHeader();
}

async function refreshBoard() {
  const scope = state.scopeSerial;
  await settleRegions([
    (async () => {
      await refreshPipelinePause();
      if (!validProject(scope) || emptyProjectRegistry()) return;
      await Promise.all([refreshSetupHealth(scope), refreshBoardEnvironment(scope)]);
    })(),
    readRegion("board-columns", async () => {
      const registry = refreshPipelinePause();
      if (emptyProjectRegistry()) {
        await registry;
        if (!validProject(scope) || emptyProjectRegistry()) return;
      }
      // Capture failures until the registry determines whether the Board needs these records.
      const records = Promise.all([
        api("/api/tasks"), api("/api/leases"), api("/api/settings"),
        api("/api/projects"), api("/api/packets"),
      ]).then(value => ({ value }), error => ({ error }));
      await registry;
      if (!validProject(scope) || emptyProjectRegistry()) return;
      const result = await records;
      if (!validProject(scope) || emptyProjectRegistry()) return;
      if (result.error) throw result.error;
      const [tasks, leases, settings, projects, packets] = result.value;
      projectReadError = "";
      projectReadAvailable = true;
      boardAvailable = true;
      boardTasks = tasks.tasks ?? [];
      boardProjects = projects.projects ?? [];
      boardPackets = packets.packets ?? [];
      state.packets = boardPackets;
      setLeases(leases, settings);
      if (state.boardOpen !== null && !boardTasks.some(t => t.id === state.boardOpen)) closeTask();
      renderBoard();
      renderReviewCount(queueStats(ownRows(state.packets), state.settings).awaiting);
      if (state.tab !== "board") return;
      boardWrites.restore();
      confirmPaneDelivery("blocked_alert", ownRows(boardTasks).filter(row => row.blocked).map(row => row.id));
      await readBoardDetail();
      if (scope === state.scopeSerial) renderTaskDialog();
    }),
    readRegion("board-project", async () => {
      projectReadiness = await api("/api/projects/readiness");
      if (scope === state.scopeSerial) renderBoard();
    }),
  ]);
}

// ----------------------------------------------------------------- 2. Collie

function sessionNode(row) {
  const select = el(
    "button",
    {
      type: "button",
      class: `session-row${row.selected ? " selected" : ""}`,
      testid: "session-row",
      "aria-pressed": row.selected ? "true" : "false",
      data: { session: row.id ?? "", status: row.status },
      title: `${row.name} · ${row.id}`,
      onclick: () => selectSession(row.id),
    },
    el("div", { class: "row" }, el("span", { class: "session-name", text: row.name })),
    el(
      "div",
      { class: "row" },
      el("span", { class: `tag tone-${row.tone}`, text: row.status }),
      row.task !== null && el("span", { class: "tag", testid: "session-task", text: `Task #${row.task}` }),
      row.discordBorn && tag("Discord", "scope"),
      row.resumable && tag("resumable"),
      el("span", { class: "when", text: row.activity || "no activity" }),
    ),
  );
  const more = el("button", {
    type: "button", class: "session-more", text: "…", testid: "session-menu-button",
    "aria-label": `Conversation options for ${row.name}`, "aria-haspopup": "menu", "aria-expanded": "false",
    disabled: state.sendInFlight || state.collieSessionBusy,
    onclick: event => openSessionMenu(row.id, event.currentTarget),
  });
  const item = el("div", { class: "session-item", data: { key: row.id }, oncontextmenu: event => {
    event.preventDefault();
    if (!state.sendInFlight && !state.collieSessionBusy) openSessionMenu(row.id, event.currentTarget.querySelector(".session-more"));
  } }, select, more);
  return item;
}

const sessionMenu = el("div", { class: "session-menu", popover: "manual", role: "menu", "aria-label": "Conversation options", testid: "session-menu" });
document.body.append(sessionMenu);
sessionMenu.addEventListener("keydown", event => {
  if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
  event.preventDefault();
  const items = [...sessionMenu.querySelectorAll('[role="menuitem"]')];
  const current = items.indexOf(document.activeElement);
  const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1
    : (current + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
  items[next]?.focus();
});
let sessionMenuOpener = null;
function closeSessionMenu() {
  sessionMenu.hidePopover();
  sessionMenuOpener?.setAttribute("aria-expanded", "false");
}
document.addEventListener("pointerdown", event => {
  if (!sessionMenu.contains(event.target) && !sessionMenuOpener?.contains(event.target)) closeSessionMenu();
});
document.addEventListener("keydown", event => {
  if (event.key === "Escape" && sessionMenu.matches(":popover-open")) {
    event.preventDefault();
    closeSessionMenu();
    sessionMenuOpener?.focus();
  }
  if (event.key === "Tab") closeSessionMenu();
});
function openSessionMenu(id, opener) {
  closeSessionMenu();
  sessionMenuOpener = opener;
  opener.setAttribute("aria-expanded", "true");
  const remove = el("button", { type: "button", role: "menuitem", text: "Delete conversation", onclick: () => {
    closeSessionMenu();
    confirmSessionDelete(id, opener);
  } });
  const rename = el("button", { type: "button", role: "menuitem", text: "Rename conversation", onclick: () => {
    closeSessionMenu();
    renameSession(id, opener);
  } });
  remove.classList.add("danger");
  sessionMenu.replaceChildren(rename, remove);
  const bounds = opener.getBoundingClientRect();
  sessionMenu.style.left = `${Math.min(bounds.left, window.innerWidth - 224)}px`;
  sessionMenu.style.top = `${Math.min(bounds.bottom + 4, window.innerHeight - 104)}px`;
  sessionMenu.showPopover();
  rename.focus();
}

function renameSession(id, opener) {
  const row = state.sessions.find(row => row.id === id);
  if (!row) return;
  const input = el("input", { id: "session-rename-input", type: "text", value: conversationName(row), required: true, testid: "session-rename-input", autocomplete: "off" });
  const notice = el("p", { role: "alert", hidden: true });
  const cancel = el("button", { type: "button", class: "btn", text: "Cancel", onclick: () => dialog.close() });
  const save = el("button", { type: "submit", class: "btn primary", text: "Save name", testid: "session-rename-save" });
  const form = el("form");
  const dialog = el("dialog", { class: "session-delete-dialog session-rename-dialog", "aria-labelledby": "session-rename-title" });
  const header = dialogHeader({ title: "Rename conversation", titleId: "session-rename-title", onClose: () => { if (!state.collieSessionBusy) dialog.close(); } });
  const closeButton = header.querySelector(".ui-dialog-close");
  dialogFrame(dialog, { header, shell: form,
    body: dialogBody({}, el("label", { for: "session-rename-input", text: "Conversation name" }), input,
      el("p", { class: "muted", text: "Use up to 100 characters. Collie will preserve this name." }), notice),
    footer: dialogFooter({ class: "session-delete-actions" }, cancel, save) });
  form.addEventListener("submit", async event => {
    event.preventDefault();
    if (state.collieSessionBusy) return;
    if (!input.value.trim() || Array.from(input.value.trim()).length > 100) {
      notice.textContent = "Use a name with 1 to 100 characters.";
      notice.hidden = false;
      input.focus();
      return;
    }
    state.collieSessionBusy = true;
    save.disabled = cancel.disabled = closeButton.disabled = input.disabled = true;
    renderCollieControls();
    try {
      const result = await api(`/api/sessions/${id}/title`, {
        method: "POST", headers: { "content-type": "application/json" }, body: JSON.stringify({ title: input.value.trim() }),
      });
      state.collieRevision++;
      state.sessions = state.sessions.map(row => row.id === id ? { ...row, title: result.title } : row);
      dialog.close();
      clearError();
    } catch (error) {
      notice.textContent = error.message;
      notice.hidden = false;
    } finally {
      state.collieSessionBusy = false;
      save.disabled = cancel.disabled = closeButton.disabled = input.disabled = false;
      renderSessions();
    }
    if (!dialog.open) await tick();
  });
  dialog.addEventListener("cancel", event => { if (state.collieSessionBusy) event.preventDefault(); });
  dialog.addEventListener("close", () => { dialog.remove(); if (opener.isConnected) opener.focus(); else ordinaryComposer.input.focus(); });
  document.body.append(dialog);
  showDialog(dialog);
  input.focus();
  input.select();
}

function confirmSessionDelete(id, opener) {
  const notice = el("p", { role: "alert", hidden: true });
  const cancel = el("button", { type: "button", class: "btn", text: "Cancel", onclick: () => dialog.close() });
  const remove = el("button", { type: "button", class: "btn danger", text: "Delete conversation", testid: "session-delete-confirm", onclick: async () => {
    if (state.sendInFlight || state.collieSessionBusy) return;
    state.collieSessionBusy = true;
    remove.disabled = cancel.disabled = closeButton.disabled = true;
    renderCollieControls();
    try {
      await api(`/api/sessions/${id}`, { method: "DELETE" });
      state.collieRevision++;
      state.sessions = state.sessions.filter(row => row.id !== id);
      if (state.selected === id) {
        state.selected = null;
        ordinaryComposer.input.value = "";
        ordinaryComposer.resize();
        ordinaryUploads.clear();
        state.collieOpened = true;
      }
      dialog.close();
      clearError();
    } catch (error) {
      notice.textContent = error.message;
      notice.hidden = false;
    } finally {
      state.collieSessionBusy = false;
      remove.disabled = cancel.disabled = closeButton.disabled = false;
      renderCollieControls();
    }
    if (!dialog.open) await tick();
  } });
  const dialog = el("dialog", { class: "session-delete-dialog", "aria-labelledby": "session-delete-title" });
  const header = dialogHeader({ title: "Delete this conversation?", titleId: "session-delete-title", onClose: () => { if (!state.collieSessionBusy) dialog.close(); } });
  const closeButton = header.querySelector(".ui-dialog-close");
  dialogFrame(dialog, { header,
    body: dialogBody({}, el("p", { text: "This removes the conversation from Collie and stops its replies. You cannot restore it." }),
      el("p", { class: "muted", text: "Culler retains internal records and files." }), notice),
    footer: dialogFooter({ class: "session-delete-actions" }, cancel, remove) });
  dialog.addEventListener("cancel", event => { if (state.collieSessionBusy) event.preventDefault(); });
  dialog.addEventListener("close", () => { dialog.remove(); if (opener.isConnected) opener.focus(); else ordinaryComposer.input.focus(); });
  document.body.append(dialog);
  showDialog(dialog);
  cancel.focus();
}

const renderMessage = createMessageRenderer({
  token: () => token, unavailable: () => systemView?.unavailable, showError,
  retry: row => systemAction({ ...row, id: `reply:${row.message_id}`, action: "Retry reply" }),
});
const messageNode = row => renderMessage(row.direction === "send" ? { ...row, body: boardMessageText(row.body) } : row, state.selected);

const commandBar = createCommandBar({
  api, renderMessage,
  openConversation: id => { state.selected = id; openPage("collie"); },
  changed: () => { requests.invalidate(); if (state.tab === "board") void tick(); },
});
$("board-command-root").replaceWith(commandBar.node);

const ordinaryComposer = createComposer({ prefix: "collie", inputID: "send-body", sendID: "send", formID: "send-form" });
$("collie-composer").replaceWith(ordinaryComposer.form);
const ordinaryUploads = ordinaryComposer.uploads;
const collieModel = createModelPicker({
  api, prefix: "collie",
  session: () => ({
    id: state.selected,
    binding: state.sessions.find(row => row.id === state.selected)?.binding,
    messages: state.collieHistorySession !== state.selected || state.collieHasMessages || state.sendInFlight,
  }),
  switchModel: switchCollieModel, onRouting: routingChanged,
  openProfiles: entry => agentProfiles.open("collie", false, entry),
});
$("collie-controls").querySelector("h3").after(...collieModel.nodes);

function collieEmpty() {
  return conversationEmpty({
    title: "What can Collie help you with?",
    description: state.selected ? "Ask a question, describe a goal, or attach files for Collie to review." : "Pick a session, or start a new one. Collie can help you explore a question or plan your next step.",
    prompts: ["Help me plan my next step.", "Help me compare two approaches.", "Ask me questions about my goal."],
    choose: async text => {
      if (!state.selected && !await newCollieSession()) return;
      const input = ordinaryComposer.input;
      if (input.disabled) return;
      input.value = text; ordinaryComposer.resize(); input.focus();
    },
  });
}

function renderCollieControls() {
  const row = state.sessions.find((s) => s.id === state.selected) ?? null;
  const compose = composeState(row);
  const connectionFailure = systemView?.snapshot?.conditions?.find(condition => condition.group === "connections" && condition.roles?.includes("collie"));
  const disabled = !compose.canSend || Boolean(connectionFailure) || state.sendInFlight || state.collieSessionBusy;
  $("send-body").disabled = disabled;
  $("send").disabled = disabled;
  ordinaryUploads.setDisabled(disabled);
  $("new-session").disabled = state.sendInFlight || state.collieSessionBusy;
  for (const button of document.querySelectorAll(".session-more")) button.disabled = state.sendInFlight || state.collieSessionBusy;
  // A packet conversation keeps the agent profile that it started with.
  collieModel.setDisabled(state.sendInFlight || state.collieSessionBusy || state.collieReplyBusy || state.collieHistorySession !== state.selected || !state.selected || row?.packet != null);
  $("resume").hidden = !compose.canResume;
  $("collie-hint").textContent = connectionFailure ? "Collie cannot reply. Repair the connection in System health." : compose.hint;
  $("collie-hint").hidden = !connectionFailure && compose.hint === "";
  $("collie-head").textContent = row
    ? row.status === "active" ? "Active session" : "Resume this session"
    : state.sessionsRead ? "No session selected" : "Loading sessions…";
}

function renderSessions() {
  const rows = sessionRows(state.sessions, Date.now(), state.selected);
  const counts = sessionStats(state.sessions);
  $("collie-stats").textContent = `${counts.total} ${counts.total === 1 ? "session" : "sessions"} · ${counts.active} active · ${counts.discord} from Discord`;
  if (!sessionMenu.matches(":popover-open") && !document.querySelector(".session-delete-dialog[open]")) {
    reconcileChildren($("session-list"), rows.length ? rows.map(sessionNode) : [el("div", { class: "empty", text: "No Collie sessions yet." })]);
  }
  renderCollieControls();
}

/** Resumes one session and records that a resume is in flight. When the
 * resume fails, the view keeps the session open and the resume button stays
 * available. */
async function resumeSession(id) {
  state.collieResumeInFlight = true;
  try {
    await api(`/api/sessions/${id}/resume`, { method: "POST" });
  } finally {
    state.collieResumeInFlight = false;
  }
}

async function refreshCollie() {
  const revision = state.collieRevision;
  void collieModel.load();
  const scope = state.scopeSerial;
  const selected = state.selected;
  const history = selected ? api(`/api/sessions/${selected}/history`).then(data => ({ data }), error => ({ error })) : null;
  const opened = state.collieOpened;
  const res = await api("/api/sessions", opened ? { maxAge: 0 } : {});
  if (scope !== state.scopeSerial || state.tab !== "collie") return;
  if (state.collieSessionBusy || revision !== state.collieRevision) return;
  state.collieOpened = false;
  state.sessionsRead = true;
  state.sessions = (res.sessions ?? []).filter(collieListed);
  if (state.selected && !state.sessions.some(row => row.id === state.selected)) {
    state.selected = null;
  }
  if (opened && !state.sessions.length) {
    if (!await newCollieSession(true)) {
      throw new Error("Collie could not prepare a conversation. Select Retry or New session.");
    }
    return;
  }
  const action = tabOpenAction(state.sessions, state.selected, state.collieResumeInFlight, Date.now(), opened);
  if (action.kind === "select" || action.kind === "resume") state.selected = action.id;
  if (action.kind !== "resume") {
    await readRegion("collie-transcript", () => refreshTranscript(selected === state.selected ? history : null));
    return;
  }
  try {
    await resumeSession(action.id);
  } catch (err) {
    // The view renders the ended session first, then re-throws so that tick() shows the error.
    await refreshTranscript();
    throw err;
  }
  const again = await api("/api/sessions");
  state.sessions = (again.sessions ?? []).filter(collieListed);
  await refreshTranscript();
}

/** Renders the session list, then reads and renders the open transcript. */
async function refreshTranscript(preloaded = null) {
  renderSessions();
  if (!state.selected) {
    state.pollTight = false;
    setChildren($("collie-transcript"),
      collieEmpty(),
    );
    return;
  }
  const requested = state.selected;
  const result = preloaded ? await preloaded : { data: await api(`/api/sessions/${requested}/history`) };
  if (result.error) throw result.error;
  const history = result.data;
  // A slow response for a previously selected session must not render under
  // the newly selected one's header.
  if (state.selected !== requested || history.session_id !== requested) return;
  state.pollTight = pollTight(history);
  // A poll that lands while a send is in flight reads a history without the
  // sent turn; the rows the send painted stay until the send returns.
  if (state.sendInFlight) return;
  const rows = transcriptRows(history, Date.now());
  state.collieReplyBusy = history.status === "active" && rows.some(row => row.provisional && row.replyState !== "failed");
  const pane = $("collie-transcript");
  const atBottom = pane.scrollHeight - pane.scrollTop - pane.clientHeight < 40;
  state.collieHistorySession = requested;
  state.collieHasMessages = rows.length > 0;
  renderCollieControls();
  collieModel.paint();
  reconcileChildren(pane, rows.length ? rows.map(messageNode) : [collieEmpty()]);
  // A conversation reads bottom-up: a new reply should be visible, but not at
  // the cost of yanking an operator who scrolled back through history.
  if (atBottom) pane.scrollTop = pane.scrollHeight;
  // History is the render source for collie_reply/collie_echo, so the drain's
  // trivial acks are bookkeeping only.
  await api("/api/outbox/drain", { method: "POST" });
}

function selectSession(id) {
  if (state.collieSessionBusy || state.sendInFlight) return;
  state.selected = id === "" || id === null ? null : id;
  setChildren($("collie-transcript"), el("div", { class: "loading", text: "Loading transcript…" }));
  clearError();
  renderCollieControls();
  tick();
}

// ---------------------------------------------------------------- 3. Review

function packetNode(view) {
  return el(
    "button",
    {
      type: "button",
      class: "packet-row",
      testid: "packet-row",
      data: { task: String(view.taskID), state: view.state },
      "aria-current": view.taskID === state.selectedPacket ? "true" : null,
      onclick: () => { if (state.approvalBusy !== view.taskID) selectPacket(view.taskID); },
    },
    el(
      "div",
      { class: "row metaline" },
      el("span", { class: "title", text: `Task #${view.taskID}` }),
      view.live
        ? el("span", { class: `tag age tone-${view.ageTone}`, testid: "packet-age", text: `${view.age} in queue` })
        : el("span", { class: "tag tone-stone", testid: "packet-decided", text: view.decidedAt }),
      view.label && el("span", { class: `tag tone-${view.tone}`, testid: "packet-state", text: view.label }),
      view.blocked && el("span", { class: "tag tone-rust", testid: "packet-blocked", text: "Decision Pending" }),
      !view.hasRender && el("span", { class: "tag tone-stone", text: "no render" }),
    ),
    (state.approvalBusy === view.taskID || view.state === "approved") && el(
      "div", { class: "packet-progress", role: "status", testid: "packet-progress" },
      el("span", { class: "packet-spinner", "aria-hidden": "true" }),
      el("span", { text: state.approvalBusy === view.taskID ? "Approval in progress" : "Merging..." }),
    ),
    el("div", { class: "packet-title", testid: "packet-title", text: view.title }),
    view.state === "approved"
      ? view.mergeState && el("div", { class: "packet-detail", testid: "packet-merge-state", text: view.mergeState })
      : view.detail && el("div", { class: "packet-detail", testid: "packet-detail", text: view.detail }),
    view.blocked && view.blockedReason && el("div", { class: "packet-detail", testid: "packet-block-reason", text: `Decision Pending: ${view.blockedReason}` }),
  );
}

function renderReview() {
  const ledger = state.packetView === "ledger";
  $("ledger-filters").hidden = !ledger;
  $("review-stats").hidden = ledger;
  $("review-title").textContent = ledger ? "Review Ledger" : "Review Queue";
  $("review-subtitle").textContent = ledger ? "The packets with a final decision." : "Review the results before changes merge.";
  const place = $("reader-empty");
  place.hidden = state.selectedPacket !== null;
  if (!reviewAvailable || !state.projectReady) {
    const waiting = state.pauseError ? "Packet data is unavailable." : "Loading packets…";
    $("review-empty").hidden = true;
    setLedgerCounts(null);
    setChildren($("review-stats"), stat("Awaiting decision", "—", { key: "awaiting" }), stat("Review slots", "—", { key: "slots" }));
    setChildren($("packets"), emptyBox(waiting, false, state.pauseError ? "failed" : "loading"));
    place.textContent = waiting;
    renderReader(null);
    return;
  }
  const now = Date.now();
  const views = packetsFor(ownRows(state.packets), state.packetView, state.ledgerFilter, now);
  const stats = queueStats(ownRows(state.packets), state.settings);
  renderReviewCount(stats.awaiting);
  setLedgerCounts(stats);
  if (!ledger) {
    setChildren($("review-stats"),
      stat("Awaiting decision", stats.awaiting, { key: "awaiting" }),
      // The review capacity applies to each Project on its own.
      stat("Review slots", stats.capKnown ? `${stats.slots}/${stats.cap}` : stats.slots, { key: "slots", alert: stats.over }),
    );
  }
  $("queue-note").textContent = ledger ? "newest decision first" : "awaiting decision first, then oldest first";
  // The Live queue's empty state is the pane's own designed one; each ledger
  // filter names its own empty state.
  $("review-empty").hidden = views.length > 0 || ledger;
  if (views.length === 0 && !ledger) setChildren($("packets"));
  else fill($("packets"), views.map(packetNode), ledgerEmptyText(state.ledgerFilter));
  place.textContent = state.approvalBusy !== null ? `Task #${state.approvalBusy}: the page sends your approval.` : views.length > 0 ? "Select a packet to read its report."
    : ledger ? ledgerEmptyText(state.ledgerFilter) : $("review-empty").textContent;
  renderReader(stats);
}

// The reader shows one packet beside the queue column; below 960 px it covers
// the window. `state.selectedPacket` names its task while it is open. The
// address names the same task, so a reload and a link open the same packet.

/** The packet row of one task in the selected Project, or null. A replaced
 * packet appears in neither view, so the reader does not show it. */
function readerRow(taskID) {
  return ownRows(state.packets).find((row) => row.task_id === taskID && row.review_state !== "replaced") ?? null;
}

const packetKey = (row) => `${row?.packet_id ?? ""}:${row?.version_id ?? ""}`;

async function readArtifactRoots() {
  if (state.artifactRoots === null) state.artifactRoots = (await api("/api/artifact-roots")).roots ?? {};
  return state.artifactRoots;
}

// The quick chat holds one Collie conversation about the open packet. At 960
// px and more it fills the chat column beside the reader; below 960 px it
// fills the Collie view of the reader. The browser keeps the open state across
// packets, pages, and reloads.
const CHAT_KEY = "mc.packet-collie";
try { state.chatOpen = localStorage.getItem(CHAT_KEY) === "open"; } catch { state.chatOpen = false; }

const packetChat = createPacketChat({
  api, renderMessage,
  projectArchived: id => state.pause?.projects.find(row => row.id === id)?.status === "archived",
  openConversation: id => { state.selected = id; openPage("collie"); },
  changed: () => { requests.invalidate(); if (state.tab === "review") void tick(); },
  close: () => {
    setChatOpen(false);
    $("packet-collie-open").focus();
  },
  onRouting: result => routingChanged(result),
  openProfiles: entry => agentProfiles.open("collie", false, entry),
});
const packetChatResize = createPacketChatResize({
  pane: $("review-pane"), column: $("packet-chat-column"), handle: $("packet-chat-resize"),
});

function setChatOpen(value) {
  state.chatOpen = value;
  try { localStorage.setItem(CHAT_KEY, value ? "open" : "closed"); } catch { /* The open state lasts until the page loads again. */ }
  applyChatLayout();
}

/** Places the quick chat in the chat column or in the Collie view, shows it
 * when it is open, and applies the queue rule. A move between the two places
 * keeps the conversation, the draft, and the keyboard focus. */
function applyChatLayout() {
  const wide = !narrowReader.matches;
  const shown = state.chatOpen && state.selectedPacket !== null;
  const host = wide ? $("packet-chat-column") : $("reader-collie-view");
  // A closed quick chat leaves the document. Its drafts and its conversation
  // stay in memory.
  if (!shown) packetChat.node.remove();
  else if (packetChat.node.parentElement !== host) {
    const focused = packetChat.node.contains(document.activeElement) ? document.activeElement : null;
    host.append(packetChat.node);
    packetChat.moved();
    focused?.focus({ preventScroll: true });
  }
  $("packet-chat-column").hidden = !(wide && shown);
  $("packet-chat-resize").hidden = !(wide && shown);
  $("packet-collie-open").setAttribute("aria-expanded", String(wide && shown));
  if (state.selectedPacket !== null) paintReaderViews();
  packetChatResize.update();
  packetChat.setOpen(shown);
}

$("packet-collie-open").addEventListener("click", () => {
  const opening = !state.chatOpen;
  setChatOpen(opening);
  if (opening) packetChat.focus();
});

let readerCovered = false;
let readerInert = [];

/** Shows the reader of the open packet or the reader place, and applies the
 * layout of the window width. A reader that covers the page makes each other
 * part of the page inert, stops the scroll of the page body, and shows the page
 * errors. The Task button sits in the reader bar beside the queue column and
 * in the view row below 960 px. */
function applyReaderMode() {
  const reader = $("packet-reader");
  const open = state.selectedPacket !== null;
  const covers = open && narrowReader.matches;
  reader.hidden = !open;
  $("reader-empty").hidden = open;
  document.documentElement.classList.toggle("reader-open", covers);
  reader.setAttribute("role", covers ? "dialog" : "region");
  if (covers) reader.setAttribute("aria-modal", "true");
  else reader.removeAttribute("aria-modal");
  for (const node of readerInert) node.inert = false;
  readerInert = [];
  if (covers) {
    const page = reader.closest(".page");
    for (let node = reader; node !== page; node = node.parentElement) {
      for (const other of node.parentElement.children) {
        if (other === node || other.tagName === "DIALOG" || other.inert) continue;
        other.inert = true;
        readerInert.push(other);
      }
    }
  }
  const actions = $("reader-actions");
  if (narrowReader.matches && actions.parentElement !== $("reader-views")) $("reader-views").querySelector(".view-tabs").after(actions);
  else if (!narrowReader.matches && actions.parentElement === $("reader-views")) $("reader-previous").parentElement.before(actions);
  applyChatLayout();
  if (covers === readerCovered) return;
  readerCovered = covers;
  if (covers) {
    if (!$("error").hidden) {
      state.readerError.page = $("error-message").textContent;
      $("error").hidden = true;
    } else if (state.dockerUnavailable) state.readerError.page = DOCKER_UNAVAILABLE;
  } else {
    // A page error that the reader shows returns to the page.
    const pageError = state.readerError.page;
    state.readerError.page = "";
    if (pageError !== "" && !state.dockerUnavailable) {
      $("error-message").textContent = pageError;
      $("error").hidden = false;
    }
  }
  renderReaderError();
}

narrowReader.addEventListener("change", () => {
  applyReaderMode();
  if (state.selectedPacket === null) return;
  if (readerCovers() && !$("packet-reader").contains(document.activeElement)) $("packet-reader").focus();
});
applyReaderMode();

/** Opens the reader for the packet of one task, or returns false when the
 * selected Project has no such packet. `entry` names the history step: "push"
 * adds an entry over the packet list, "replace" swaps the entry of the open
 * reader, and "none" keeps the history: the address already names the packet,
 * or the caller owns the step. */
function openReader(taskID, entry) {
  const row = readerRow(taskID);
  if (!row) return false;
  const focused = document.activeElement;
  const hash = packetHash(taskID);
  if (entry === "push") {
    history.pushState(null, "", hash);
    state.readerPushed = history.length;
  } else if (entry === "replace") history.replaceState(null, "", hash);
  else state.readerPushed = 0;
  const opening = state.selectedPacket === null;
  state.selectedPacket = taskID;
  state.readerWanted = null;
  if (opening) applyReaderMode();
  loadReader(row);
  if (!narrowReader.matches) $("packets").querySelector(`[data-testid="packet-row"][data-task="${taskID}"]`)?.scrollIntoView({ block: "nearest" });
  // A reader that covers the page takes the focus. Beside the queue column the
  // focus stays on its control until that control becomes unavailable.
  const reader = $("packet-reader");
  const lost = focused?.disabled || document.activeElement !== focused;
  if (readerCovers() ? opening || !reader.contains(focused) || lost : reader.contains(focused) && lost) reader.focus();
  return true;
}

/** Hides the reader view. The caller owns the history step. A page error that
 * the reader shows returns to the page. */
function hideReader(restoreFocus = true) {
  const taskID = state.selectedPacket;
  if (taskID === null) return;
  const focused = document.activeElement;
  const hadFocus = focused === null || focused === document.body || $("packet-reader").contains(focused);
  state.selectedPacket = null;
  state.readerPushed = 0;
  ++state.packetLoadSerial;
  state.selectedReviewLoaded = false;
  state.selectedReviewIdentity = null;
  state.readerKey = null;
  state.readerRow = null;
  state.pendingDecision = null;
  $("packet-reason").value = "";
  setReaderView("packet", false);
  changeView.clear();
  packetChat.setPacket(null);
  $("packet-render").replaceChildren();
  applyReaderMode();
  state.readerError = { action: "", page: "" };
  renderReaderError();
  renderReview();
  if (restoreFocus && hadFocus) {
    const row = $("packets").querySelector(`[data-testid="packet-row"][data-task="${taskID}"]`);
    (row ?? $("packets")).focus();
  }
}

// A report frame adds a step to the browser history for each in-page link it
// follows. Those steps sit between the reader entry and the list entry, and a
// step of a removed frame does nothing on Browser Back. The Navigation API
// shows the list entry past these steps. Without it, the page compares the
// history length.

/** The list entry directly under the current entry in this document, or null. */
function listEntryBelow() {
  const nav = window.navigation;
  if (!nav?.currentEntry || typeof nav.entries !== "function") return null;
  const below = nav.entries()[nav.currentEntry.index - 1];
  if (!below?.sameDocument || !below.url) return null;
  const route = parseRoute(new URL(below.url).hash);
  return route?.page === "review" && route.rest.length === 0 ? below : null;
}

const settled = (result) => {
  result?.committed?.catch(() => {});
  result?.finished?.catch(() => {});
  return result;
};

/** Closes the reader. The history returns to the list entry directly below,
 * or else the address of the packet list replaces the reader entry. */
function closeReader() {
  if (state.selectedPacket === null) return;
  const below = listEntryBelow();
  // Without the Navigation API, Back shows the list entry only from a reader
  // entry that this page pushed, with no frame steps above it.
  const back = !window.navigation && state.readerPushed === history.length && packetRoute(location.hash) !== null;
  hideReader();
  if (below) settled(window.navigation.traverseTo(below.key));
  else if (back) history.back();
  else if (location.hash !== routeHash("review")) history.replaceState(null, "", routeHash("review"));
}

/** Moves the reader to another packet. With the list entry directly below,
 * the history returns to that entry and adds one reader entry, so the steps of
 * the report frame leave the history. Otherwise the reader entry changes in
 * place. */
function stepReader(taskID) {
  const below = listEntryBelow();
  if (!below) {
    openReader(taskID, "replace");
    return;
  }
  // The address names the packet list until the new entry exists. The reader
  // stays open through that address.
  const step = ++state.readerStepping;
  settled(window.navigation.traverseTo(below.key)).committed
    .then(() => {
      if (state.selectedPacket !== taskID) return;
      history.pushState(null, "", packetHash(taskID));
      state.readerPushed = history.length;
    }, () => {
      if (state.selectedPacket === taskID && packetRoute(location.hash) !== null) history.replaceState(null, "", packetHash(taskID));
    })
    .finally(() => { if (state.readerStepping === step) state.readerStepping = 0; });
  openReader(taskID, "none");
}

/** Follows the packet that the address names: a reload, a link, or a step
 * through the browser history. */
function followReaderRoute() {
  if (state.readerStepping) return;
  const taskID = packetRoute(location.hash);
  if (taskID === null) {
    state.readerWanted = null;
    hideReader();
    openFirstPacket();
    return;
  }
  if (taskID === state.selectedPacket) return;
  state.readerWanted = taskID;
  if (reviewAvailable && state.projectReady) openWantedReader();
}

/** Opens the reader that an address asked for once the packet list is read.
 * An address that names a task without a packet shows the packet list. */
function openWantedReader() {
  const taskID = state.readerWanted;
  if (taskID === null || state.tab !== "review") return;
  state.readerWanted = null;
  if (openReader(taskID, "none")) return;
  hideReader();
  if (packetRoute(location.hash) !== null) history.replaceState(null, "", routeHash("review"));
}

/** Opens the packet of a card. With an open packet, the reader moves as the
 * previous and next controls move it. */
function selectPacket(taskID) {
  if (decisionSent() || taskID === state.selectedPacket) return;
  if (state.selectedPacket === null) openReader(taskID, "push");
  else stepReader(taskID);
}

/** Opens the first packet of the list after the operator opens the Review page
 * or selects another Project, once the page reads the list and the address
 * names the list entry. Below 960 px the page opens a packet only when the
 * operator selects one. */
function openFirstPacket() {
  if (!state.readerAuto || state.tab !== "review" || !reviewAvailable || !state.projectReady) return;
  if (location.hash !== routeHash("review")) return;
  state.readerAuto = false;
  if (state.selectedPacket !== null || narrowReader.matches) return;
  const first = firstPacket(ownRows(state.packets), state.packetView, state.ledgerFilter);
  if (first !== null) openReader(first, "push");
}

/** After a view change or a Ledger filter change, the reader shows the first
 * packet of the new list unless the new list holds the open packet. */
function followPacketList() {
  if (decisionSent() || narrowReader.matches || !reviewAvailable || !state.projectReady) return;
  const rows = ownRows(state.packets);
  const taskID = state.selectedPacket;
  if (taskID !== null && readerPosition(rows, state.packetView, state.ledgerFilter, taskID).position !== "") return;
  const first = firstPacket(rows, state.packetView, state.ledgerFilter);
  if (first !== null) selectPacket(first);
  else closeReader();
}

/** Reads and paints the report of the open packet. It runs once for each
 * packet version, so the poll keeps the frame and its scroll. */
async function loadReader(row) {
  const taskID = row.task_id;
  const serial = ++state.packetLoadSerial;
  const view = packetView(row, Date.now());
  state.readerRow = row;
  void evaluationControl.refresh(row);
  state.readerKey = packetKey(row);
  state.selectedReviewIdentity = row.packet_id ? { packet_id: row.packet_id, version_id: row.version_id } : null;
  state.selectedReviewLoaded = false;
  state.pendingDecision = null;
  state.readerError.action = "";
  $("packet-reason").value = "";
  setReaderView("packet", false);
  changeView.open(row, view);
  packetChat.setPacket(row);
  paintReportState(row, view, "loading");
  renderReview();
  if (view.expired || !view.hasRender) {
    // An expired report and an absent report are recorded states, not
    // failures: the report area shows the packet records without a report.
    readArtifactRoots().then(() => { if (serial === state.packetLoadSerial) renderReview(); }, () => {});
    return;
  }
  try {
    await readArtifactRoots();
    const identity = row.packet_id ? `?packet=${row.packet_id}&version=${row.version_id}` : "";
    const { body, type } = await apiBytes(`/api/packets/${taskID}/render${identity}`);
    if (serial !== state.packetLoadSerial) return;
    paintReport(taskID, body, type);
    state.selectedReviewLoaded = true;
    renderReview();
  } catch (err) {
    if (serial !== state.packetLoadSerial) return;
    paintReportState(row, view, "failed");
    showReaderError(err);
  }
}

let evaluationReport = null;
const evaluationControl = createEvaluationControl({ host: $("evaluation-control"), api, report: () => evaluationReport });

function paintReport(taskID, body, type) {
  evaluationReport = null;
  const identity = state.readerKey;
  const preserveDisplay = wrapper => {
    crypto.subtle.digest("SHA-256", new TextEncoder().encode(body)).then(bytes => {
      if (state.readerKey === identity) evaluationReport = { hash: Array.from(new Uint8Array(bytes), b => b.toString(16).padStart(2, "0")).join(""), wrapper };
    });
  };
  const host = $("packet-render");
  host.setAttribute("aria-busy", "false");
  const mode = renderMode(type, body);
  host.classList.toggle("framed", mode === "html");
  if (mode === "html") {
    const wrapper = reportDocument(body);
    preserveDisplay(wrapper);
    // The report (HTML, media inline), read inside its own sandboxed,
    // network-dead frame — agent bytes never become THIS page's markup.
    // srcdoc rather than src so the frame needs no second authenticated
    // fetch; the wrapper's CSP travels inside the document.
    host.replaceChildren(el("iframe", {
      class: "render-frame",
      testid: "packet-frame",
      sandbox: FRAME_SANDBOX,
      referrerpolicy: "no-referrer",
      title: `Review packet for task #${taskID}`,
      srcdoc: wrapper,
    }));
    return;
  }
  // Container paths the report cites are translated to host paths the
  // operator can actually open; an unmapped target is left exactly as the
  // role wrote it.
  const translated = hostPaths(body, state.artifactRoots);
  host.replaceChildren(mode === "markdown"
    ? el("div", { class: "md render-md", testid: "packet-markdown" }, markdownNodes(parseMarkdown(translated)))
    : el("pre", { class: "render-body", text: translated }));
  host.scrollTop = 0;
  const copy = host.cloneNode(true);
  const originalNodes = host.querySelectorAll("*");
  const properties = ["color", "background-color", "font-family", "font-size", "font-weight", "font-style", "line-height", "letter-spacing", "margin", "padding", "border", "border-radius", "white-space", "overflow-wrap", "max-width"];
  copy.querySelectorAll("*").forEach((node, index) => {
    const style = getComputedStyle(originalNodes[index]);
    for (const property of properties) node.style.setProperty(property, style.getPropertyValue(property));
  });
  preserveDisplay(reportDocument(copy.innerHTML));
}

const reportSection = (title, ...children) =>
  el("section", { class: "report-section" }, el("h3", { text: title }), children);

/** The report area while the report loads: a status line over placeholder
 * lines that hold the shape of a report until its bytes arrive. */
const reportSkeleton = () => el("div", { class: "report-loading", testid: "packet-loading" },
  el("div", { class: "report-loading-status", role: "status" },
    el("span", { class: "packet-spinner", "aria-hidden": "true" }),
    el("span", { text: "Loading packet…" })),
  el("div", { class: "report-skeleton", "aria-hidden": "true" },
    el("span", { class: "skeleton-line heading" }),
    ...["full", "full", "most", "half"].map(width => el("span", { class: `skeleton-line ${width}` })),
    el("span", { class: "skeleton-line heading" }),
    ...["full", "most", "full", "half"].map(width => el("span", { class: `skeleton-line ${width}` }))));

/** The report area without a report: the read, the absent report, the failed
 * read, or the expired report. Each state except the read shows the opening
 * text of the packet under its heading. */
function paintReportState(row, view, read) {
  const area = reportArea(view, read);
  const host = $("packet-render");
  const retry = () => {
    const current = readerRow(row.task_id);
    if (current) loadReader(current);
  };
  const lead = {
    loading: reportSkeleton(),
    missing: emptyBox("No report exists yet for this packet.", true),
    failed: el("div", { class: "empty failed tall" }, el("p", { text: "The report is unavailable." }),
      el("button", { type: "button", class: "btn small", text: "Retry", onclick: retry })),
    expired: el("p", { class: "report-expired", testid: "packet-expired", text: `The packet report expired on ${view.expiredAt}.` }),
  }[area.kind];
  host.classList.remove("framed");
  host.setAttribute("aria-busy", String(area.kind === "loading"));
  host.replaceChildren(...[
    lead,
    area.thesis && reportSection(view.openingLabel, view.strategist
      ? el("div", { class: "md", testid: "packet-opening" }, markdownNodes(parseMarkdown(view.thesis)))
      : el("p", { testid: "packet-opening", text: view.thesis })),
    area.records && reportSection("Criteria", criteriaNode(row)),
  ].filter(Boolean));
  host.scrollTop = 0;
}

function criteriaNode(row) {
  const text = typeof row?.criteria === "string" ? row.criteria.trim() : "";
  if (text === "") return emptyBox("The packet records no criteria.");
  return el("div", { class: "md" }, markdownNodes(parseMarkdown(text)));
}

const changeView = createChangeView({
  read: (path, signal) => api(path, { maxAge: 0, signal }),
  readText: (path, signal) => api(path, { bytes: true, maxAge: 0, signal }).then((result) => result.body),
});

const READER_VIEWS = { packet: "packet-render", changes: "changes-view", collie: "reader-collie-view" };

/** Shows one view in the content area: the packet, the files changed, or,
 * below 960 px with an open quick chat, the Collie view. Each other view keeps
 * its content and its position, and takes no input. */
function paintReaderViews() {
  const shown = state.chatOpen && narrowReader.matches ? "collie" : state.readerView;
  for (const [key, id] of Object.entries(READER_VIEWS)) {
    const on = key === shown;
    $(id).classList.toggle("view-inactive", !on);
    $(id).inert = !on;
    const tab = document.querySelector(`[data-reader-view="${key}"]`);
    tab.setAttribute("aria-selected", String(on));
    tab.tabIndex = on ? 0 : -1;
  }
  if (shown === "changes") changeView.activate();
  else changeView.deactivate();
}

/** Selects the Packet view or the Files changed view. */
function setReaderView(name, moveFocus = true) {
  state.readerView = name;
  paintReaderViews();
  if (moveFocus) document.querySelector(`[data-reader-view="${name}"]`).focus();
}

/** Follows a view selection of the operator. Below 960 px "Collie" opens the
 * quick chat, and "Packet" and "Files changed" close it. */
function selectReaderView(name, moveFocus) {
  if (name === "collie") {
    setChatOpen(true);
    if (moveFocus) $("view-collie").focus();
    return;
  }
  if (narrowReader.matches && state.chatOpen) setChatOpen(false);
  setReaderView(name, moveFocus);
}

// The view tabs follow the tab pattern: the arrow keys, Home, and End move
// the selection between the visible views. A pointer or Enter selection of
// "Collie" moves the focus to the composer.
for (const tab of document.querySelectorAll("[data-reader-view]")) {
  tab.addEventListener("click", () => {
    selectReaderView(tab.dataset.readerView, false);
    if (tab.dataset.readerView === "collie") packetChat.focus();
  });
  tab.addEventListener("keydown", (event) => {
    const names = Object.keys(READER_VIEWS)
      .filter((name) => document.querySelector(`[data-reader-view="${name}"]`).getClientRects().length > 0);
    const at = names.indexOf(tab.dataset.readerView);
    const next = { ArrowRight: at + 1, ArrowLeft: at - 1, Home: 0, End: names.length - 1 }[event.key];
    if (next === undefined) return;
    event.preventDefault();
    selectReaderView(names[(next + names.length) % names.length], true);
  });
}

/** Whether the page sends a decision for the packet of the open reader. */
const decisionSent = () => state.decisionBusy !== null && state.decisionBusy === state.selectedPacket;

/** Paints the parts of the open reader that follow the poll: the reader bar,
 * the view row, the Files changed view, and the decision bar. `stats` is null
 * while the packet list is unknown. */
function renderReader(stats) {
  const taskID = state.selectedPacket;
  if (taskID === null) return;
  const known = stats !== null;
  const current = known ? readerRow(taskID) : null;
  if (known && current === null) {
    // The packet left the selected Project, or a revision replaced it.
    closeReader();
    return;
  }
  if (current && packetKey(current) !== state.readerKey) {
    loadReader(current);
    return;
  }
  const row = current ?? state.readerRow;
  if (row) void evaluationControl.refresh(row);
  if (!row) return;
  const view = packetView(row, Date.now());
  if (current) {
    state.readerRow = current;
    changeView.refresh(current, view);
  }
  const changes = changesLabel(row);
  if ($("view-changes").textContent !== changes) $("view-changes").textContent = changes;
  const ledger = state.packetView === "ledger";
  const sending = decisionSent();
  $("reader-close").disabled = sending;
  $("task-open").disabled = sending;
  $("reader-close-label").textContent = ledger ? "Review Ledger" : "Review Queue";
  $("reader-close-count").hidden = ledger || !known;
  $("reader-close-count").textContent = known ? String(stats.awaiting) : "";
  const place = known
    ? readerPosition(ownRows(state.packets), state.packetView, state.ledgerFilter, taskID)
    : { position: "", previous: null, next: null };
  state.readerPlace = place;
  $("reader-previous").disabled = sending || place.previous === null;
  $("reader-next").disabled = sending || place.next === null;
  $("reader-position").textContent = place.position;
  $("reader-position").hidden = place.position === "";
  const heading = view.title === "" ? `Task #${taskID}` : `Task #${taskID} · ${view.title}`;
  $("reader-title").textContent = heading;
  $("reader-title").title = heading;
  packetChat.setPacket(row);
  setChildren($("reader-tags"),
    !view.live && el("span", { class: "tag tone-stone", text: view.decidedAt }),
    view.label && el("span", { class: `tag tone-${view.tone}`, testid: "reader-state", text: view.label }),
    view.blocked && el("span", { class: "tag tone-rust", text: "Decision Pending" }),
  );
  renderDecisionBar(view, row, known);
  renderReaderError();
}

function renderDecisionBar(view, row, known) {
  const decision = $("packet-decision");
  decision.textContent = view.decision;
  decision.hidden = view.decision === "";
  const block = $("reader-block-reason");
  block.textContent = view.blocked && view.blockedReason ? `Decision Pending: ${view.blockedReason}` : "";
  block.hidden = block.textContent === "";
  renderDecide(view, row, known);
}

function renderDecide(view, row, known) {
  const decide = $("packet-decide");
  decide.hidden = !view.decidable;
  const sending = state.decisionBusy !== null;
  // The decision identifies the exact packet version, so the controls wait
  // for the report of that version.
  const loaded = known && state.selectedReviewLoaded &&
    state.selectedReviewIdentity?.packet_id === row.packet_id &&
    state.selectedReviewIdentity?.version_id === row.version_id;
  if (decide.hidden || !(loaded || sending)) state.pendingDecision = null;
  const armed = state.pendingDecision;
  for (const decision of ["approve", "revise", "cancel"]) {
    $(`packet-${decision}`).disabled = !loaded || sending;
    $(`packet-${decision}`).classList.toggle("armed", armed === decision);
  }
  // The reason field EXISTS only for revise and cancel — the affordance is the
  // verb's rule made visible. Approve has none to leave blank.
  const needsReason = armed !== null && reasonRequired(armed);
  $("reason-block").hidden = !needsReason;
  $("decide-abandon").hidden = armed === null;
  $("decide-submit").hidden = !needsReason;
  $("decide-abandon").disabled = sending;
  $("decide-submit").disabled = sending;
  $("packet-reason").readOnly = sending;
  $("reason-label").textContent = needsReason
    ? `Reason for the ${{ revise: "revision", cancel: "cancellation" }[armed] ?? armed} (required)`
    : "Reason";
  $("decide-note").textContent = sending
    ? "The page sends the decision."
    : view.expired
      ? "A decision requires the report of the packet. The report of this packet expired."
      : !view.hasRender
        ? "A decision requires the report of the packet. This packet has no report."
        : !loaded
          ? "The decision controls become available after the report loads."
          : needsReason
            ? "Culler records the reason as written. Send it when it says what to change."
            : "";
  $("decide-note").hidden = $("decide-note").textContent === "";
  // Focus follows the ARMING, not the poll: stealing the caret every ten
  // seconds would make the reason box impossible to type in.
  if (needsReason && state.focusReason) {
    state.focusReason = false;
    $("packet-reason").focus();
  }
}

/** The errors of the reader appear inside the reader: the errors of its own
 * actions, and the page errors while it covers the page. */
function renderReaderError() {
  const message = state.readerError.action || state.readerError.page;
  $("reader-error-message").textContent = message;
  $("reader-error").hidden = message === "";
}

function showReaderError(err) {
  state.readerError.action = String(err instanceof Error ? err.message : err);
  renderReaderError();
}

function clearReaderError() {
  state.readerError = { action: "", page: "" };
  renderReaderError();
}

/** Sends one decision. While the page sends it, the reader keeps the packet
 * and the reason. A reader that shows another packet names the task of a
 * failed decision. */
async function submitDecision(taskID, decision) {
  const scope = state.scopeSerial;
  if (!readerRow(taskID) || state.decisionBusy !== null) return;
  const payload = decisionPayload(decision, $("packet-reason").value);
  if (payload === null) {
    // Affordance-level only: the verb's own rules stay the authority when the
    // operator is creative, but an empty box is an abandoned decision.
    showReaderError(`A reason is required to ${decision}.`);
    return;
  }
  const focused = document.activeElement;
  const identity = state.selectedReviewIdentity;
  state.decisionBusy = taskID;
  if (decision === "approve") {
    state.approvalBusy = taskID;
    state.readerAuto = false;
    hideReader(false);
    history.replaceState(null, "", routeHash("review"));
    $("packets").focus();
  }
  renderReview();
  if (decision !== "approve" && focused?.disabled) $(reasonRequired(decision) ? "packet-reason" : "packet-reader").focus();
  try {
    await api(`/api/packets/${taskID}/decide`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ ...payload, ...identity }),
    });
    if (scope !== state.scopeSerial) return;
    clearError();
    if (decision === "approve") {
      ++reviewReadSerial;
      const row = readerRow(taskID);
      if (row) Object.assign(row, {
        review_state: "approved",
        decided_at: new Date().toISOString(),
        merge_state_text: "Approval saved. The change awaits a merge.",
      });
      state.approvalBusy = null;
      renderReview();
      if (state.selectedPacket === null && state.tab === "review" && location.hash === routeHash("review")) {
        const next = packetsFor(ownRows(state.packets), "live", state.ledgerFilter, Date.now())
          .find((view) => view.state === "awaits_decision");
        if (next && !narrowReader.matches) openReader(next.taskID, "push");
        (state.selectedPacket === null ? $("packets") : $("packet-reader")).focus();
      } else renderReview();
    }
    try {
      await refreshReview();
    } catch (err) {
      if (scope === state.scopeSerial) showError(`The decision was saved. Packet data is unavailable. ${err instanceof Error ? err.message : err}`, { read: true });
    }
    if (document.activeElement === document.body || document.activeElement === null) {
      (state.selectedPacket === null ? $("packets") : $("packet-reader")).focus();
    }
  } catch (err) {
    if (scope !== state.scopeSerial) return;
    const message = String(err instanceof Error ? err.message : err);
    if (state.selectedPacket === taskID) showReaderError(message);
    else showError(`The decision for Task #${taskID} failed. ${message}`);
  } finally {
    if (state.approvalBusy === taskID) state.approvalBusy = null;
    if (state.decisionBusy === taskID) state.decisionBusy = null;
    renderReview();
    if (state.selectedPacket === taskID && document.activeElement === $("packet-reader") && focused?.isConnected && !focused.disabled) focused.focus();
  }
}

let reviewReadSerial = 0;

async function refreshReview() {
  const serial = ++reviewReadSerial;
  const scope = state.scopeSerial;
  const [packets, leases, settings] = await Promise.all([
    api("/api/packets"),
    api("/api/leases"),
    api("/api/settings"), refreshPipelinePause(),
  ]);
  if (!validProject(scope) || serial !== reviewReadSerial) return;
  reviewAvailable = true;
  state.packets = packets.packets ?? [];
  setLeases(leases, settings);
  renderReview();
  openWantedReader();
  openFirstPacket();
  if (state.boardOpen === null) return;
  await readBoardDetail();
  if (scope === state.scopeSerial) renderTaskDialog();
}

for (const decision of ["approve", "revise", "cancel"]) {
  $(`packet-${decision}`).addEventListener("click", () => {
    if (state.selectedPacket === null) return;
    if (!reasonRequired(decision)) {
      submitDecision(state.selectedPacket, decision);
      return;
    }
    state.pendingDecision = decision;
    state.focusReason = true;
    renderReview();
  });
}

$("decide-submit").addEventListener("click", () => {
  if (state.selectedPacket === null || state.pendingDecision === null) return;
  submitDecision(state.selectedPacket, state.pendingDecision);
});

function abandonDecision() {
  const armed = state.pendingDecision;
  state.pendingDecision = null;
  $("packet-reason").value = "";
  renderReview();
  if (armed !== null) $(`packet-${armed}`).focus();
}

$("decide-abandon").addEventListener("click", abandonDecision);
$("reader-close").addEventListener("click", () => { if (!decisionSent()) closeReader(); });
$("reader-previous").addEventListener("click", () => {
  if (!decisionSent() && state.readerPlace?.previous != null) stepReader(state.readerPlace.previous);
});
$("reader-next").addEventListener("click", () => {
  if (!decisionSent() && state.readerPlace?.next != null) stepReader(state.readerPlace.next);
});
// The dialog opens after the read, with the full row. A failed read stays in
// the reader. A packet that the operator left keeps no dialog.
$("task-open").addEventListener("click", async () => {
  const taskID = state.selectedPacket;
  if (taskID === null || decisionSent()) return;
  try {
    const row = await api(`/api/tasks/${taskID}`);
    if (state.selectedPacket === taskID) openTask(taskID, row);
  } catch (err) {
    showReaderError(err);
  }
});
$("reader-error-close").addEventListener("click", clearReaderError);

// Escape acts on the first case that applies: an empty reason field closes,
// and a reason field with text keeps its text. Otherwise a reader that covers
// the page closes through the same history step as the close control. Escape
// does nothing while the page sends a decision. The Review page acts only on a
// key press on the page or on no control; a key press inside the report frame
// stays in the frame.
document.addEventListener("keydown", (event) => {
  if (event.key !== "Escape" || event.defaultPrevented || state.selectedPacket === null || document.querySelector("dialog[open]")) return;
  if (event.target !== document.body && !$("review-pane").contains(event.target)) return;
  if (decisionSent()) {
    event.preventDefault();
    return;
  }
  if (state.pendingDecision !== null) {
    if ($("packet-reason").value.trim() === "") abandonDecision();
  } else if (readerCovers()) closeReader();
  else return;
  event.preventDefault();
});

/** Switches the Review page between the Live queue and the ledger. */
function setPacketView(view) {
  state.packetView = view;
  for (const button of document.querySelectorAll("[data-view]")) {
    button.classList.toggle("on", button.dataset.view === view);
    button.setAttribute("aria-pressed", String(button.dataset.view === view));
  }
}

/** Shows the packet count of each ledger filter in its option, or no count while the read is unavailable. */
function setLedgerCounts(stats) {
  const counts = stats ? { approved: stats.approved, cancelled: stats.cancelled, all: stats.approved + stats.cancelled } : {};
  for (const filter of LEDGER_FILTERS) $(`ledger-count-${filter}`).textContent = String(counts[filter] ?? "");
}

/** Selects the ledger filter. The selection lasts until the page loads again. */
function setLedgerFilter(filter) {
  state.ledgerFilter = filter;
  for (const button of document.querySelectorAll("[data-filter]")) {
    button.classList.toggle("on", button.dataset.filter === filter);
    button.setAttribute("aria-pressed", String(button.dataset.filter === filter));
  }
}

for (const button of document.querySelectorAll("[data-view]")) {
  button.addEventListener("click", () => {
    setPacketView(button.dataset.view);
    renderReview();
    followPacketList();
  });
}

for (const button of document.querySelectorAll("[data-filter]")) {
  button.addEventListener("click", () => {
    setLedgerFilter(button.dataset.filter);
    renderReview();
    followPacketList();
  });
}

// Pipeline activity

function eventNode(row, fresh) {
  return el(
    "div",
    {
      class: `event${row.tone === "rust" ? " err" : ""}${fresh ? " fresh" : ""}`,
      testid: "activity-row",
      data: { key: row.id, kind: row.kind },
    },
    el("span", { class: "when", text: row.age, title: row.stamp }),
    el(
      "span",
      { class: "what" },
      el("span", { class: `kind tone-${row.tone}`, text: row.kind }),
      document.createTextNode(" "),
      el("span", { class: "actor", text: row.actor }),
      row.subject !== null && document.createTextNode(` · ${row.subject}`),
      row.detail && el("div", { class: "detail", text: row.detail, title: row.detail }),
    ),
  );
}

function renderStream(containerID, countID, rows, fresh, emptyText) {
  const container = $(containerID);
  const now = Date.now();
  const stats = streamStats(rows, now);
  $(countID).textContent =
    stats.count === 0 ? "" : `${stats.count} ${stats.count === 1 ? "row" : "rows"} · ${stats.actors} ${stats.actors === 1 ? "actor" : "actors"}${stats.newest ? ` · newest ${stats.newest}` : ""}`;
  keepScroll(container, () => {
    const names = { connections: systemView.snapshot?.connections ?? [], projects: state.pause?.projects ?? [] };
    fill(container, activityRows(rows, now, names).map((row) => eventNode(row, fresh.has(row.id))), emptyText);
  });
}

function renderActivityTabs() {
  for (const button of document.querySelectorAll("[data-activity-stream]")) {
    const stream = button.dataset.activityStream;
    const selected = stream === state.activityStream;
    button.setAttribute("aria-selected", String(selected));
    button.tabIndex = selected ? 0 : -1;
    $(`activity-${stream}`).hidden = !selected;
    $(`${stream}-count`).hidden = !selected;
    const total = stream === "events" ? state.activityRows.length : state.activityErrors.length;
    $(`activity-${stream}-total`).textContent = state.activityRead ? String(total) : "—";
    $(`activity-${stream}-total`).classList.toggle("attention", stream === "errors" && total > 0);
  }
}

function confirmActivityDelivery() {
  if (state.tab === "pipeline" && state.projectReady && state.activityStream === "errors" && state.activityTail) {
    confirmPaneDelivery("health", state.activityErrors.map((row) => row.id));
  }
}

for (const button of document.querySelectorAll("[data-activity-stream]")) {
  button.addEventListener("click", () => {
    state.activityStream = button.dataset.activityStream;
    renderActivityTabs();
    confirmActivityDelivery();
  });
  button.addEventListener("keydown", (event) => {
    let stream;
    if (event.key === "Home") stream = "events";
    else if (event.key === "End") stream = "errors";
    else if (event.key === "ArrowLeft" || event.key === "ArrowRight") stream = state.activityStream === "events" ? "errors" : "events";
    else return;
    event.preventDefault();
    const target = $(`activity-tab-${stream}`);
    target.focus();
    target.click();
  });
}

function resetActivity(eventsText, errorsText) {
  ++state.activitySerial;
  state.activityRead = false;
  state.activityRows = [];
  state.activityErrors = [];
  const paused = state.activityTail ? null : "The live tail is paused.";
  renderStream("activity-events", "events-count", [], new Set(), eventsText ?? paused ?? "No events for this Project.");
  renderStream("activity-errors", "errors-count", [], new Set(), errorsText ?? paused ?? "No dispatch errors for this Project.");
  renderActivityTabs();
}

async function refreshActivity() {
  const scope = state.scopeSerial;
  await refreshPipelinePause();
  if (!validProject(scope)) return;
  if (!state.project) { resetActivity("Select a Project to read its events.", "Select a Project to read its dispatch errors."); return; }
  if (!state.activityTail) return;
  const serial = ++state.activitySerial;
  const kind = state.activityKind.trim();
  const current = () => scope === state.scopeSerial && serial === state.activitySerial && state.tab === "pipeline" && state.activityTail;
  let res;
  try {
    res = await api(
      kind === ""
        ? `/api/activity?project=${encodeURIComponent(state.project)}&limit=${ACTIVITY_LIMIT}`
        : `/api/activity?project=${encodeURIComponent(state.project)}&kind=${encodeURIComponent(kind)}&limit=${ACTIVITY_LIMIT}`,
    );
  } catch (err) {
    if (!current()) return;
    resetActivity(emptyBox("Events are unavailable.", false, "failed"), emptyBox("Dispatch errors are unavailable.", false, "failed"));
    throw err;
  }
  if (!current()) return;
  const rows = res.activity ?? [];
  const datalist = $("activity-kinds");
  datalist.replaceChildren(...kindOptions(rows).map((k) => el("option", { value: k })));

  const { events, errors } = splitStreams(rows);
  // The first response shows history. Later responses highlight new records.
  const firstPaint = state.activityRows.length === 0 && state.activityErrors.length === 0;
  const freshEvents = firstPaint ? new Set() : new Set(newRowIDs(state.activityRows, events));
  const freshErrors = firstPaint ? new Set() : new Set(newRowIDs(state.activityErrors, errors));
  state.activityRead = true;
  state.activityRows = mergeRows(state.activityRows, events).slice(0, TAIL_CAP);
  state.activityErrors = mergeRows(state.activityErrors, errors).slice(0, TAIL_CAP);

  renderStream("activity-events", "events-count", state.activityRows, freshEvents, "No events match this selection.");
  renderStream("activity-errors", "errors-count", state.activityErrors, freshErrors, "No dispatch errors match this selection.");
  renderActivityTabs();
  confirmActivityDelivery();
}

$("activity-kind").addEventListener("change", () => {
  state.activityKind = $("activity-kind").value;
  resetActivity();
  tick();
});

$("activity-tail").addEventListener("change", () => {
  state.activityTail = $("activity-tail").checked;
  ++state.activityRevision;
  ++state.activitySerial;
  if (state.activityTail) tick();
});

// Pipeline overview

/** The flow map's browser state: the last computed view (so a resize can
 * redraw without a fetch), the rows the view was computed from (so a route
 * change can recompute it from the verb's response), the enabled bindings,
 * the role whose role detail is open, the state of its custom instructions,
 * and the state of one route change. `wikiStates` holds the wiki state rows
 * of all Projects. It is null when the dashboard cannot read them. */
const flow = { loaded: false, view: null, layout: null, selected: null, instructions: null, bindings: [], runs: [], tasks: [], wikiStates: null, pickBusy: false, pickError: null };
const agentProfiles = createAgentProfiles({ api, systemState: () => systemView, onConnectionChanged: () => {
  requests.invalidate(path => path === "/api/system/status");
  void systemStatus.refresh();
}, onAssigned: routingChanged });
function routingChanged(result) {
  routingRoutes = result.routes ?? [];
  flow.bindings = result.bindings ?? [];
  if (flow.view) renderTopology(pipelineFlowmap(routingRoutes, Date.now()), flow.bindings);
  renderConfigs();
  vision.setRouting(result);
  collieModel.set(result);
  packetChat.setRouting(result);
}
let systemPaint = "";
let noticePaint = "";
let doctorPaint = "";
let doctorPaintState = "unchecked";
let profileHealthPaint = "";
let issuesDialog = null;
let issuesPaint = "";
let diagnosticDialog = null;
let diagnosticPaint = "";
const systemStatus = createSystemStatus({
  request: (signal) => api("/api/system/status", { signal }),
  changed: view => { systemView = view; renderSystemHealth(); renderViewers(); if (state.boardOpen !== null) renderTaskDialog(); },
});
void systemStatus.refresh();
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") void systemStatus.refresh();
});
window.addEventListener("focus", () => {
  if (!document.hidden) {
    requests.invalidate(path => path === "/api/system/status");
    void systemStatus.refresh();
  }
});

const systemActionRequests = new Map();
async function systemAction(condition) {
  const key = `${condition.id}:${condition.diagnostic_id ?? condition.attempt_id ?? condition.run_id ?? ""}`;
  if (!systemActionRequests.has(key)) systemActionRequests.set(key, crypto.randomUUID().replaceAll("-", ""));
  const requestID = systemActionRequests.get(key);
  try {
    if (condition.action === "Resolve proposal decision") {
      const {dialog,body}=systemDialog("Resolve proposal decision");
      const reason=el("textarea",{class:"field",required:true,"aria-label":"Decision resolution"});
      const error=el("p",{role:"alert",hidden:true});
      const submit=el("button",{type:"submit",class:"btn primary",text:"Resolve decision"});
      const form=el("form",{id:"resolve-proposal-form"},el("p",{text:condition.condition}),reason,error);
      submit.setAttribute("form", "resolve-proposal-form");
      const cancel=el("button",{type:"button",class:"btn",text:"Cancel",onclick:()=>dialog.close()});
      dialog.querySelector(".ui-dialog-shell").append(dialogFooter({},cancel,submit));
      form.addEventListener("submit",async event=>{
        event.preventDefault();if(!reason.value.trim())return;submit.disabled=true;
        try{
          await api("/api/system/resolve-proposals",{method:"POST",body:JSON.stringify({project_id:condition.project_id,decision_id:condition.proposal_decision_id,reason:reason.value})});
          dialog.close();await systemStatus.refresh();await refreshBoard();
        }catch(err){error.hidden=false;error.textContent=err.message;submit.disabled=false;}
      });
      body.append(form);reason.focus();return;
    }
    if (condition.action === "Retry connection") await api("/api/connection/retry", { method: "POST", body: JSON.stringify({connection_id:condition.connection_id,check_id:requestID}) });
    else if (condition.connection_id) await agentProfiles.repair(condition.connection_id, condition.action, condition.profile_id, condition.provider_error_code);
    else if (condition.action === "Resume Project infrastructure") {
      await api("/api/system/resume-infrastructure", {
        method: "POST", body: JSON.stringify({ project_id: condition.project_id, request_id: requestID }),
      });
      systemActionRequests.delete(key);
    }
    else if (condition.action === "Retry reply") {
      await api("/api/collie/reply-retry", { method: "POST", body: JSON.stringify({
        session_id: condition.session_id, message_id: condition.message_id, request_id: requestID,
      }) });
      if (state.tab === "vision") await vision.refresh();
      else if (state.tab === "board") commandBar.refresh();
      else if (state.tab === "review" && packetChat.shows(condition.session_id)) packetChat.refresh();
      else openPage("collie");
    } else {
      openPage("board");
      if (condition.project_id) selectProject(condition.project_id);
      if (condition.task_id) openTask(condition.task_id);
    }
    await systemStatus.refresh();
  } catch (err) { showError(err); }
}

function systemDialog(title, label = title) {
  const opener = document.activeElement;
  const body = dialogBody({ class: "system-dialog-body" });
  const dialog = el("dialog", { class: "system-details", "aria-label": label });
  const header = dialogHeader({ title, titleId: "system-dialog-title", onClose: () => dialog.close() });
  dialogFrame(dialog, { header, body });
  dialog.addEventListener("close", () => { dialog.remove(); if (opener?.isConnected && !document.querySelector("dialog[open]")) opener.focus(); });
  dialog.addEventListener("click", event => { if (event.target === dialog) dialog.close(); });
  document.body.append(dialog);
  showDialog(dialog);
  return { dialog, body };
}

function renderSystemIssues() {
  if (!issuesDialog?.dialog.isConnected) return;
  const conditions = systemIssues(systemView.snapshot?.conditions);
  const key = JSON.stringify([conditions, systemView.pending, systemView.unavailable]);
  if (key === issuesPaint) return;
  issuesPaint = key;
  setChildren(issuesDialog.body,
    el("p", { class: "system-helper", role: "status", text: systemView.unavailable
      ? "Status unavailable. These are the last known issues."
      : conditions.length ? "These issues require attention. Each repair applies to the issue beside it." : "No current issues." }),
    systemView.unavailable && systemView.lastCompleteAt && el("p", { class: "system-helper", text: `Last confirmed: ${stampLabel(systemView.lastCompleteAt)}.` }),
    systemConditionNodes(conditions,
      condition => { issuesDialog.dialog.close(); return systemAction(condition); },
      condition => { issuesDialog.dialog.close(); void systemDetails(condition); }, conditionCollieButton));
}
function openSystemIssues() {
  const conditions = systemIssues(systemView.snapshot?.conditions);
  if (conditions.length === 1 && !systemView.unavailable) { void systemDetails(conditions[0]); return; }
  if (!conditions.length) { openPage("configs", ["system"]); return; }
  issuesDialog = systemDialog("Issues that need attention", "System issues");
  issuesPaint = "";
  renderSystemIssues();
}

function renderDiagnosticState() {
  if (!diagnosticDialog?.dialog.isConnected) return;
  const { condition, current } = diagnosticDialog;
  const snapshot = systemView.snapshot;
  const known = [...(snapshot?.conditions ?? []), ...(snapshot?.connections ?? [])].find(item => item.id === condition.id);
  const lastKnown = known ?? (systemView.unavailable ? diagnosticDialog.lastKnown : null);
  if (known || !systemView.unavailable) diagnosticDialog.lastKnown = known ?? null;
  const key = JSON.stringify([lastKnown, systemView.unavailable]);
  if (key === diagnosticPaint) return;
  diagnosticPaint = key;
  const summary = known && conditionSummary(known) !== known.name ? conditionSummary(known) : "Requires attention";
  const title = systemView.unavailable ? "Status unavailable" : known ? summary : "Issue cleared";
  const impact = systemView.unavailable
    ? `Last known: ${lastKnown ? conditionSummary(lastKnown) : "no current issue"}. Culler cannot confirm the current status.`
    : known ? conditionImpact(known) : "No current issue remains for this record.";
  const detail = lastKnown ?? condition;
  if (diagnosticDialog.facts) setChildren(diagnosticDialog.facts, ...conditionFacts(detail, state.pause?.projects).children);
  setChildren(current,
    el("span", { class: "system-state", role: "status", text: title }),
    detail.group === "connections" && el("dl", { class: "system-context" },
      ...[
        ["Agent profile", detail.profile_id && profileName(detail)],
        ["Model", detail.model],
        ["Roles that use this profile", detail.roles?.join(", ")],
      ].filter(([, value]) => value).map(([label, value]) => [el("dt", { text: label }), el("dd", { text: value })])),
    !systemView.unavailable && known && conditionExplanation(known) && el("p", { class: "system-cause", text: conditionExplanation(known) }),
    el("p", { text: impact }),
    lastKnown && affectedProposalNodes(lastKnown, condition => {
      diagnosticDialog.dialog.close(); return systemAction(condition);
    }),
    lastKnown && lastKnown.state !== "ready" && repairText(lastKnown) && el("p", { class: "system-repair-text", text: repairText(lastKnown) }),
    el("div", { class: "system-repair" },
      lastKnown && lastKnown.state !== "ready" && actionControl(lastKnown) && systemActionButton(lastKnown,
        condition => { diagnosticDialog.dialog.close(); return systemAction(condition); }),
      diagnosticButton({ ...conditionDiagnostic(detail, systemView.unavailable),
        ...(!known && !systemView.unavailable ? { state: "Issue cleared" } : {}) }, askDiagnostic),
      !systemView.unavailable && known && conditionRetry(known) && el("span", { class: "system-helper", text: conditionRetry(known) })));
}

function systemDetails(condition) {
  const { dialog, body } = systemDialog(condition.name, "Issue details");
  const current = el("section", { class: "system-detail-current" });
  const facts = conditionFacts(condition, state.pause?.projects);
  const technical = facts.children.length > 0 && el("details", { class: "system-disclosure" },
    el("summary", { text: "Technical details" }), facts);
  const history = el("button", { class: "system-history-link", type: "button", text: condition.connection_id ? "View connection history" : "View diagnostic history",
    onclick: () => { dialog.close(); void systemHistory(condition); } });
  body.append(current);
  if (technical) body.append(technical);
  body.append(history);
  diagnosticDialog = { dialog, current, condition, facts, lastKnown: condition };
  diagnosticPaint = "";
  renderDiagnosticState();
}

async function systemHistory(condition) {
  const { body } = systemDialog(`${condition.name} history`, "Diagnostic history");
  const content = el("div", { class: "system-history", text: "Loading diagnostic history…" });
  body.append(content);
  try {
    const query = new URLSearchParams();
    if (condition.connection_id) query.set("connection", condition.connection_id);
    if (condition.attempt_id) query.set("attempt", condition.attempt_id);
    if (condition.project_id) query.set("project", condition.project_id);
    const events = el("ol", { class: "system-history-list" });
    const empty = el("p", { class: "system-helper", text: "No completed attempts in these records.", hidden: true });
    const activity = el("details", { class: "system-disclosure" }, el("summary", { text: "Refresh activity" }));
    const period = el("select", { class: "field", "aria-label": "Refresh period" },
      el("option", { value: "day", text: "Last 24 hours" }), el("option", { value: "week", text: "Last 7 days" }));
    const totals = el("div", { class: "system-refresh-summary" });
    activity.append(period, totals);
    let outcomes = {};
    const renderTotals = () => {
      const total = outcomes[period.value] ?? {};
      const rejections = total.provider_rejections ?? 0;
      const transport = total.transport_failures ?? 0;
      setChildren(totals,
        el("p", { text: refreshSummary(total) }),
        el("small", { text: `${rejections} provider ${rejections === 1 ? "rejection" : "rejections"} · ${transport} transport ${transport === 1 ? "failure" : "failures"}` }));
    };
    period.addEventListener("change", renderTotals);
    const more = el("button", { type: "button", class: "btn", text: "More history" });
    const retry = el("button", { type: "button", class: "btn", text: "Retry history", hidden: true });
    const error = el("p", { class: "system-helper", role: "status", hidden: true });
    setChildren(content, condition.connection_id && activity, events, empty, error, retry, more);
    const load = async () => {
      more.disabled = true;
      retry.disabled = true;
      error.textContent = "Loading history…";
      error.hidden = false;
      retry.hidden = true;
      try {
        const result = await api(`/api/system/diagnostics?${query}`);
        error.hidden = true;
        outcomes = result.refresh_outcomes ?? {};
        renderTotals();
        const operations = { token_refresh: "Credential refresh", model_access: "Model access", connection_access: "Connection check", strategist: "Strategist", collie_reply: "Collie reply" };
        const outcomesText = { ready: "Verified", failed: "Failed", succeeded: "Succeeded", resumed: "Resumed" };
        for (const event of diagnosticResults(result.events ?? [])) events.append(el("li", { class: "system-history-event" },
          el("div", { class: "system-history-heading" },
            el("strong", { text: operations[event.operation] ?? event.operation.replaceAll("_", " ") }),
            el("span", { class: `system-state${event.outcome === "failed" ? " needs-attention" : ""}`, text: outcomesText[event.outcome] ?? event.outcome })),
          (event.http_status || event.provider_error_code)
            ? el("p", { class: "system-helper", text: [event.http_status && `Response ${event.http_status}`, event.provider_error_code].filter(Boolean).join(" · ") })
            : event.condition && el("p", { text: event.condition }),
          event.resolved_diagnostic_id && el("p", { class: "system-helper", text: `Resolved failure #${event.resolved_diagnostic_id}. Attempt: ${event.run_id || event.reply_attempt_id}.` }),
          el("time", { datetime: event.observed_at, text: stampLabel(event.observed_at) })));
        empty.hidden = events.children.length > 0;
        more.hidden = !result.next_cursor || (result.events ?? []).length < 100;
        if (result.next_cursor) query.set("after", String(result.next_cursor));
      } catch (err) {
        error.textContent = err.message;
        error.hidden = false;
        retry.hidden = false;
        more.hidden = true;
      } finally {
        more.disabled = false;
        retry.disabled = false;
      }
    };
    more.onclick = load;
    retry.onclick = load;
    await load();
  } catch (err) { content.textContent = err.message; }
}

function renderSystemHealth() {
  const conditions = systemIssues(systemView.snapshot?.conditions);
  renderSystemIssues();
  renderDiagnosticState();
  // Poll timestamps do not change the notice or replace its focused controls.
  const deployment = doctorView(state.doctor).state;
  const key = JSON.stringify([conditions, systemView.snapshot?.connections, systemView.recovered, systemView.pending, systemView.unavailable, state.project, deployment]);
  if (key === systemPaint) return;
  systemPaint = key;
  const profileHealth = JSON.stringify([systemView.unavailable, systemView.snapshot?.connections]);
  if (profileHealth !== profileHealthPaint) { profileHealthPaint = profileHealth; agentProfiles.refreshHealth(); }
  const nodes = (items) => systemConditionNodes(items, systemAction, systemDetails, conditionCollieButton);
  const noticeKey = JSON.stringify([conditions, systemView.recovered, systemView.pending, systemView.unavailable]);
  if (noticeKey !== noticePaint) {
    noticePaint = noticeKey;
    setChildren($("system-notices"), systemNoticeNode(systemView, systemAction, openSystemIssues, conditionCollieButton));
  }
  const summary = $("system-health-summary");
  summary.textContent = systemView.pending ? "Loading status…" : systemView.unavailable ? "Status unavailable" : conditions.length
    ? `${conditions.length} ${conditions.length === 1 ? "issue needs" : "issues need"} attention`
    : deployment === "failing" ? "Deployment requires attention" : deployment === "unavailable" ? "Deployment status unavailable" : "No current issues";
  summary.classList.toggle("needs-attention", conditions.length > 0 || deployment === "failing");
  summary.classList.toggle("is-ready", !conditions.length && deployment !== "failing" && deployment !== "unavailable" && !systemView.unavailable);
  const groups = connectionGroups(systemView.snapshot?.connections ?? conditions.filter(item => item.group === "connections"), conditions);
  setChildren($("system-connections"), groups.attention.length ? nodes(groups.attention) : !groups.connected.length && el("p", { class: "system-helper",
    text: systemView.pending ? "Loading connection status…" : systemView.unavailable ? "Connection status unavailable." : "No connections are in use." }));
  $("system-connection-more").hidden = !groups.connected.length && !groups.unused.length;
  $("system-connection-summary").textContent = connectionDisclosureLabel(groups);
  setChildren($("system-connection-rest"), nodes(groups.connected), nodes(groups.unused));
  const execution = conditions.filter(item => item.group === "execution");
  $("system-execution-section").hidden = execution.length === 0;
  setChildren($("system-execution"), nodes(execution));
  renderCollieControls();
  if ($("board-system-status")) setChildren($("board-system-status"), nodes(conditions.filter(item => item.project_id === state.project && item.id.startsWith("strategist:"))));
}

$("pipeline-profiles").addEventListener("click", () => agentProfiles.open());
$("configs-profiles").addEventListener("click", () => agentProfiles.open());
const vision = createVision({
  api, host: $("vision-pane"),
  createConversation: projectID => createConversation({
    api, renderMessage, projectID, onRouting: routingChanged, openProfiles: entry => agentProfiles.open("collie", false, entry),
  }),
  onSaved: async () => { await refreshPipelinePause(true); },
  onStarted: id => {
    openPage("board", [], false);
    // The Board rows predate the start and show an incomplete initialization.
    if (id === state.project) { resetProjectView(); void tick(); } else selectProject(id);
  },
});
const environmentPage = createEnvironment({
  api, host: $("environment-pane"),
  askDiagnostic,
  openConfigs: () => openPage("configs", ["projects"]),
  openSetupHistory: id => {
    openPage("board", [], false);
    selectProject(id);
    boardJournalOpen = true;
    renderBoard(); renderHeader();
    window.scrollTo(0, 0);
    $("board-setup").querySelector("h1")?.focus({ preventScroll: true });
    void tick();
  },
  onChanged: () => refreshPipelinePause(true),
});
const roleCustomization = createRoleCustomization({ api, openCollie: () => openPage("collie") });
const projectControls = createProjectControls({
  api,
  openModels: () => agentProfiles.open(), openVision: id => openPage("vision", [id]),
  onSaved: async (id) => {
    await refreshPipelinePause(true);
    selectProject(id);
    await refreshBoard();
    if (state.tab === "configs") await refreshConfigs();
  },
});
$("project-add").addEventListener("click", () => { closeProjectPanel(false); projectControls.open(); });
$("configs-add-project").addEventListener("click", () => projectControls.open());
async function setRoute(role, binding) {
  flow.pickBusy = true;
  flow.pickError = null;
  renderConfigRoute();
  try {
    const res = await api("/api/routing", { method: "POST", body: JSON.stringify({ role, binding, expected: routingRoutes.find(row => row.role === role)?.binding }) });
    routingRoutes = res.routes ?? [];
    flow.bindings = res.bindings ?? [];
    vision.setRouting(res);
    collieModel.set(res);
  } catch (err) {
    flow.pickError = err.message;
  } finally {
    flow.pickBusy = false;
    renderConfigs();
  }
}

/** The wiki state row of the selected Project. The result is null when the
 * dashboard cannot read the wiki state. It is undefined before the first
 * read, and when the read holds no row for the Project: the read omits an
 * archived Project. */
function projectWikiState() {
  if (!flow.loaded) return undefined;
  return flow.wikiStates === null ? null : ownRows(flow.wikiStates)[0];
}

/** The Pipeline graph of the selected Project. Collie is outside the pipeline,
 * so the graph omits Collie. */
function pipelineFlowmap(routes, now) {
  const topology = topologyView(ownRows(state.leases), flow.runs, routes, now, state.settings, projectWikiState());
  topology.nodes = topology.nodes.filter((node) => node.role !== "collie");
  return flowmapView(topology, flow.tasks, state.pause?.projects, {
    loaded: flow.loaded, leases: ownRows(state.leases), runs: flow.runs, nowMs: now, wikiState: projectWikiState(),
    paused: state.pause?.projects?.some(project => project.id === state.project && project.status === "paused"),
  });
}

/** Reads the state of the custom instructions that the role detail shows. */
async function readRoleInstructions(role) {
  const project = state.project;
  const current = () => flow.selected === role && state.project === project;
  let instructions = { text: "", blocked: false };
  try {
    const data = await api(`/api/projects/role-show?project=${encodeURIComponent(project)}&role=${encodeURIComponent(customizationRole(role))}`, { maxAge: 0 });
    const blocked = data.conflicts.some((conflict) => conflict.blocked);
    instructions = { text: blocked ? "Customization conflict" : data.current.instructions ? `Revision ${data.current.revision}` : "None", blocked };
  } catch {
    // The row stays without a state. The dialog of the row reports the failure.
  }
  if (!current()) return;
  flow.instructions = instructions;
  paintFlowmap();
}

/** The content that a role dialog receives while a child dialog is open. */
const heldRoleContent = new WeakMap();

/** The dialog stays mounted when a poll replaces its content. */
function createRoleDialog(onClose, opener) {
  const dialog = el("dialog", { class: "role-dialog", "aria-labelledby": "role-dialog-title" });
  dialog.addEventListener("click", event => {
    if (event.target !== dialog) return;
    const box = dialog.getBoundingClientRect();
    if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) dialog.close();
  });
  dialog.addEventListener("close", () => {
    const origin = opener()?.dataset.testid;
    heldRoleContent.delete(dialog);
    dialog.replaceChildren();
    onClose();
    if (origin) document.querySelector(`[data-testid="${CSS.escape(origin)}"]`)?.focus({ preventScroll: true });
  });
  // The close event does not bubble, so the capture phase observes each child dialog.
  document.addEventListener("close", event => {
    if (event.target === dialog || !heldRoleContent.has(dialog)) return;
    updateRoleDialog(dialog, heldRoleContent.get(dialog));
  }, true);
  document.body.append(dialog);
  return dialog;
}

function updateRoleDialog(dialog, content) {
  // A child dialog retains its controls until it returns focus to this dialog.
  // The dialog shows the most recent content when the child dialog closes.
  if (dialog.open && [...document.querySelectorAll("dialog:modal")].some(other => other !== dialog)) {
    heldRoleContent.set(dialog, content);
    return;
  }
  heldRoleContent.delete(dialog);
  const focused = dialog.contains(document.activeElement) ? document.activeElement : null;
  const hook = focused?.dataset.testid;
  setChildren(dialog, content);
  if (!dialog.open) showDialog(dialog);
  else if (focused) (hook ? dialog.querySelector(`[data-testid="${CSS.escape(hook)}"]`) : dialog.querySelector("button"))?.focus({ preventScroll: true });
}

const pipelineRoleDialog = createRoleDialog(() => {
  flow.selected = null;
  flow.instructions = null;
  paintFlowmap();
}, () => $("pipeline-topology").querySelector(`[data-testid="topology-node-${flow.selected}"]`));

function selectRole(role) {
  flow.selected = role;
  flow.instructions = null;
  paintFlowmap();
  void readRoleInstructions(role);
}

function paintFlowmap() {
  const container = $("pipeline-topology");
  if (flow.view === null) return;
  flow.layout = layoutFor(container.clientWidth);
  const selected = flow.view.nodes.find((n) => n.role === flow.selected) ?? null;
  // A poll builds the graph again. The control with the focus keeps the focus.
  const held = container.contains(document.activeElement) ? document.activeElement.dataset.testid : null;

  setChildren(
    container,
    el(
      "div",
      { class: `flowmap ${flow.layout}` },
      el("div", { class: "pipeline-now" },
        flowRunNode(flow.view, run => traceLinkNode(run, systemView?.viewers, viewersPending(), "pipeline-current-trace")),
        flowWikiNode(flow.view, selectRole, flow.selected)),
      flowmapNode(flow.view, flow.layout, selectRole, flow.selected),
    ),
  );
  if (selected) updateRoleDialog(pipelineRoleDialog, flowPopoverNode(selected, {
    instructions: flow.instructions,
    onClose: () => pipelineRoleDialog.close(),
    onProfile: (role, entry) => agentProfiles.open(role, false, entry),
    onInstructions: (role, entry) => roleCustomization.open(state.project, role, { origin: entry, onClose: () => readRoleInstructions(role) }),
  }));
  else if (pipelineRoleDialog.open) pipelineRoleDialog.close();
  if (held) container.querySelector(`[data-testid="${CSS.escape(held)}"]`)?.focus({ preventScroll: true });
}

function renderTopology(view, bindings) {
  flow.view = view;
  flow.bindings = bindings;
  // The disclosure is rebuilt whole, so a poll would close an open picker.
  // While the picker has focus the paint waits; the next poll or the
  // operator's own selection paints the stored view.
  if (document.activeElement?.closest?.('[data-testid="topology-route-picker"]')) return;
  paintFlowmap();
}

// A resize updates the card layout without moving the dialog.
new ResizeObserver(() => {
  if (flow.view === null) return;
  if (layoutFor($("pipeline-topology").clientWidth) !== flow.layout) paintFlowmap();
}).observe($("pipeline-topology"));

function renderLag(view) {
  setChildren($("pipeline-outbox"), 
    el(
      "div",
      { class: "metaline" },
      el("span", {
        class: `chip ${view.total === 0 ? "" : "attention"}`.trim(),
        text: view.capped ? `${view.total}+ undelivered (poll limit reached)` : `${view.total} undelivered`,
      }),
    ),
    el(
      "table",
      { class: "table" },
      el("thead", {}, el("tr", {}, el("th", { text: "surface" }), el("th", { text: "undelivered" }), el("th", { text: "oldest" }))),
      el(
        "tbody",
        {},
        view.rows.map((row) =>
          el(
            "tr",
            { testid: "lag-row" },
            el("td", { text: row.surface }),
            el("td", {}, el("span", { class: `tag tone-${row.tone}`, text: row.capped ? `${row.undelivered}+` : String(row.undelivered) })),
            el("td", { class: "mono", text: row.oldest ?? "—" }),
          ),
        ),
      ),
    ),
  );
}

function openRunDiagnostics(runID) {
  closeTask();
  state.logsUnit = RUN_LANE;
  state.logsRun = runID;
  state.logsTail = TAIL_DEFAULT;
  fillLogStreamPicker();
  $("logs-unit").value = RUN_LANE;
  openPage("logs", [RUN_LANE, runID]);
}

function renderRuns(rows) {
  pipelineRunRows = rows;
  if (rows.length === 0) {
    setChildren($("pipeline-runs"), emptyBox("No pipeline runs yet."));
    return;
  }
  setChildren($("pipeline-runs"),
    el(
      "table",
      { class: "table" },
      el(
        "thead",
        {},
        el("tr", {}, el("th", { text: "when" }), el("th", { text: "role" }), el("th", { text: "subject" }), el("th", { text: "result" }), el("th", { text: "" })),
      ),
      el(
        "tbody",
        {},
        rows.map((run) =>
          el(
            "tr",
            { testid: "run-row", data: { key: run.id } },
            el("td", { title: run.stamp ?? "", text: agoLabel(run.age) || "—" }),
            el("td", {}, el("span", { class: "tag", title: run.id ?? "", text: run.role ?? "-" })),
            el("td", { class: "mono", text: run.subject === null ? "—" : `#${run.subject}` }),
            el("td", { class: run.wikiUpdate ? "run-wiki-update" : null },
              el("span", { class: `tag tone-${run.tone}`, text: run.result }),
              run.detail && el("p", { testid: "run-wiki-cause", text: run.detail })),
            el(
              "td",
              { class: "run-links" },
              run.id !== null &&
                el("button", {
                  type: "button",
                  class: "btn small ghost",
                  testid: "run-log-link",
                  title: `Read run ${run.id}'s captured diagnostics`,
                  text: "log →",
                  onclick: () => openRunDiagnostics(run.id),
                }),
              run.id !== null && traceLinkNode(run, systemView.viewers, viewersPending(), "run-trace-link"),
              run.id !== null && run.tier === "pipeline" && el("button", { type: "button", class: "btn small", text: "Run instructions", onclick: () => roleCustomization.showRun(run.id) }),
            ),
          ),
        ),
      ),
    ),
  );
}

function healthStatusNode(label, detail, tone = "") {
  return el("div", { class: `system-check-status ${tone}` },
    el("strong", { text: label }),
    detail && el("p", { text: detail }));
}

function findingRows(findings, testid) {
  return el("div", { class: "system-check-list" }, findings.map(finding =>
    el("div", { class: "system-check", testid },
      el("span", { class: `tag tone-${finding.tone}`, text: finding.label }),
      el("div", {}, el("strong", { text: finding.check }), el("p", { text: finding.detail ?? "" })))));
}

function renderDoctor() {
  renderSystemHealth();
  const view = doctorView(state.doctor);
  const key = JSON.stringify([view, state.doctorPending]);
  if (key === doctorPaint) return;
  doctorPaint = key;
  const pane = $("deployment-status");
  $("deployment-findings").hidden = view.state === "unchecked";
  if (view.state === "unchecked") {
    setChildren(pane, state.doctorPending
      ? healthStatusNode("Deployment check in progress", "Culler checks the installation and its services.")
      : healthStatusNode("Deployment not checked", "Check the installation and its services."));
  } else {
    const label = view.state === "ok" ? "Deployment checks passed" : view.state === "unavailable" ? "Deployment status unavailable" : "Deployment requires attention";
    const checked = stampLabel(view.checkedAt);
    setChildren(pane, healthStatusNode(label,
      view.state === "unavailable" ? `The inspection did not complete.${checked ? ` Last result: ${checked}.` : ""}` : checked ? `Checked ${checked}.` : "Check time unavailable.",
      view.state === "ok" ? "is-ready" : "needs-attention"),
      view.state !== "ok" && diagnosticButton({ title: label, state: view.state, recorded_at: view.checkedAt,
        summary: [view.summary, ...view.findings.filter(item => item.failing).map(item => `${item.check}: ${item.detail}`)].join("\n"),
      }, askDiagnostic, "deployment-ask-collie"));
    setChildren($("deployment-checks"),
      el("p", { class: "system-helper", text: view.summary }),
      findingRows(view.findings, "doctor-finding"));
    if (view.state === "failing" && doctorPaintState !== "failing") $("deployment-findings").open = true;
  }
  doctorPaintState = view.state;
  const links = degradedUnits(view, state.leases, Date.now(), state.settings, state.pause?.projects);
  setChildren($("doctor-links"), ...links.map(link => el("button", {
    type: "button", class: "btn small", testid: `logs-link-${link.unit}`, title: link.reason,
    text: `${link.unit} error log`,
    onclick: () => { state.logsUnit = link.unit; state.logsStream = link.stream; fillLogStreamPicker(); openPage("logs", [link.unit, link.stream]); },
  })));
}

function renderHealth(view) {
  if (view.state === "unchecked") {
    setChildren($("resident-status"), healthStatusNode("Resident status unavailable", "No current observation."));
    setChildren($("resident-findings"));
    setChildren($("pipeline-health-attention"));
    return;
  }
  setChildren($("resident-status"), healthStatusNode(
    view.state === "ok" ? "Resident available" : view.state === "unavailable" ? "Resident status unavailable" : "Resident requires attention",
    view.summary, view.state === "ok" ? "is-ready" : "needs-attention"),
    view.state !== "ok" && diagnosticButton({ title: "Resident requires attention", state: view.state,
      summary: view.summary,
    }, askDiagnostic, "resident-ask-collie"));
  setChildren($("resident-findings"), findingRows(view.findings, "health-finding"));
  setChildren($("pipeline-health-attention"), view.attention.length > 0 &&
    el("div", { class: "logpane healthlines" }, view.attention.map(line =>
      el("div", { class: "healthline", testid: "health-attention-line", text: line }))));
}

async function refreshPipeline() {
  const results = await Promise.allSettled([readRegion("pipeline-topology", refreshPipelineOverview), readRegion("activity-events", refreshActivity)]);
  const failure = results.find((result) => result.status === "rejected");
  if (failure) throw failure.reason;
}

async function refreshPipelineOverview() {
  const scope = state.scopeSerial;
  // The page shows each other region when the wiki state read fails.
  const [tasks, leases, settings, packets, runs, routing, wikiStates] = await Promise.all([
    api("/api/tasks"), api("/api/leases"), api("/api/settings"),
    api("/api/packets"), api("/api/runs"), api("/api/routing"),
    api("/api/wiki-states").then((data) => data.wiki_states ?? [], () => null), refreshPipelinePause(),
  ]);
  if (!validProject(scope)) return;
  setLeases(leases, settings);
  const now = Date.now();
  flow.loaded = true;
  flow.runs = ownRows(runs.runs);
  flow.tasks = ownRows(tasks.tasks);
  flow.wikiStates = wikiStates;
  renderTopology(pipelineFlowmap(routing.routes ?? [], now), routing.bindings ?? []);
  state.packets = packets.packets ?? [];
  renderReviewCount(queueStats(ownRows(state.packets), state.settings).awaiting);
  renderRuns(runsView(flow.runs, now));
}

// Only Configs shows the deployment evidence, so only Configs runs the inspection.
const deploymentCheck = createDeploymentCheck({
  request: () => api("/api/doctor", { signal: AbortSignal.timeout(30_000) }),
  changed: (doctor, pending) => { state.doctor = doctor; state.doctorPending = pending; renderDoctor(); },
  hidden: () => document.hidden || document.body.classList.contains("access-denied") || state.tab !== "configs",
});
document.addEventListener("visibilitychange", () => deploymentCheck.wake());

$("doctor-run").addEventListener("click", async () => {
  const button = $("doctor-run");
  button.disabled = true;
  button.textContent = "Checking…";
  try {
    await deploymentCheck.refresh();
    await systemStatus.refresh();
    clearError();
    await refreshConfigs();
  } catch (err) {
    showError(err);
  } finally {
    button.disabled = false;
    button.textContent = "Re-check deployment";
  }
});

let releaseCheckActive = false;
async function checkRelease() {
  if (releaseCheckActive) return;
  releaseCheckActive = true;
  $("release-check").disabled = true;
  $("release-check").textContent = "Check in progress…";
  let result;
  try { result = await api("/api/releases/status", { maxAge: 0, cache: "no-store", signal: AbortSignal.timeout(30_000) }); }
  catch { result = { state: "unknown" }; }
  finally {
    releaseCheckActive = false;
    $("release-check").disabled = false;
    $("release-check").textContent = "Check for updates";
  }
  const view = releaseView(result);
  setChildren($("release-status"),
    el("strong", { text: view.title }),
    el("p", { class: "system-helper", title: view.installed, text: `Installed: ${view.installedLabel}` }),
    el("p", { class: "system-helper", title: view.available, text: `Available: ${view.availableLabel}` }),
    el("p", { class: "system-helper", text: view.detail }),
    view.preparation && el("p", { class: "system-helper", text: view.preparation }),
    view.workflowURL && el("a", { href: view.workflowURL, target: "_blank", rel: "noopener noreferrer", text: "View release workflow" }));
}
$("release-check").addEventListener("click", checkRelease);
$("release-open").addEventListener("click", async () => {
  const button = $("release-open");
  button.disabled = true;
  try {
    const result = await api("/api/releases/open", { method: "POST", body: JSON.stringify({ confirm: true }) });
    $("release-action-status").textContent = result.detail;
  } catch (error) {
    $("release-action-status").textContent = "The installer could not open. Run install.sh from Terminal.";
  } finally { button.disabled = false; }
});

// --------------------------------------------------------------- 6. Configs

let configSection = "system";
let discordAvailable = null;
const configSections = new Set(["system", "agents", "projects", "discord", "advanced"]);
function selectConfigSection(section, focus = false) {
  if (section === "discord" && discordAvailable === false) section = "system";
  configSection = configSections.has(section) ? section : "system";
  for (const link of document.querySelectorAll("[data-config-section]")) {
    const active = link.dataset.configSection === configSection;
    if (active) link.setAttribute("aria-current", "page");
    else link.removeAttribute("aria-current");
    $("config-section-" + link.dataset.configSection).hidden = !active;
  }
  if (focus) $("config-" + configSection + "-title").focus();
}

const discordPanel = createDiscordPanel({ root: $("discord-content"), api, onAvailable(available) {
  discordAvailable = available;
  document.querySelector('[data-config-section="discord"]').hidden = !available;
  if (!available && configSection === "discord") {
    selectConfigSection("system");
    history.replaceState(null, "", "#configs/system");
  }
} });

const configDrafts = new Map();
let configSaveBusy = false;
let configGeneration = 0;

function setConfigDraft(knob, value) {
  const previous = configDrafts.get(knob.key);
  configDrafts.set(knob.key, { key: knob.key, value, expected: previous?.expected ?? knob.rawValue });
  $("configs-save").disabled = configSaveBusy;
  $("configs-discard").disabled = configSaveBusy;
  $("configs-save-status").textContent = "You have unsaved changes.";
  $("configs-draft-indicator").hidden = false;
}

function knobNode(knob) {
  const draft = configDrafts.get(knob.key);
  const value = draft ? draft.value : knob.rawValue;
  const id = `config-input-${knob.key}`;
  let input;
  if (knob.inputType === "boolean") {
    input = el("input", { id, type: "checkbox", checked: value, disabled: configSaveBusy, "aria-label": knob.label,
      onchange: (event) => setConfigDraft(knob, event.target.checked) });
  } else if (knob.inputType === "patterns") {
    const patterns = structuredClone(value);
    input = el("div", { id, class: "config-patterns", role: "group", "aria-label": knob.label });
    const redraw = () => {
      input.replaceChildren(...patterns.map((pattern, index) => el("div", { class: "config-pattern" },
        el("select", { class: "field", disabled: configSaveBusy, "aria-label": `Rule ${index + 1} type`,
          onchange: (event) => { patterns[index].kind = event.target.value; setConfigDraft(knob, patterns); } },
          knob.options.map((option) => el("option", { value: option, selected: option === pattern.kind, text: option === "component" ? "Path component" : "File name pattern" }))),
        el("input", { class: "field", value: pattern.pattern, disabled: configSaveBusy, "aria-label": `Rule ${index + 1} pattern`,
          oninput: (event) => { patterns[index].pattern = event.target.value; setConfigDraft(knob, patterns); } }),
        el("button", { class: "btn small", type: "button", text: "Remove", disabled: configSaveBusy, "aria-label": `Remove rule ${index + 1}`,
          onclick: () => { patterns.splice(index, 1); setConfigDraft(knob, patterns); redraw(); } }),
      )), el("button", { class: "btn small", type: "button", text: "Add rule", disabled: configSaveBusy,
        onclick: () => { patterns.push({ kind: "component", pattern: "" }); setConfigDraft(knob, patterns); redraw(); } }));
    };
    redraw();
  } else if (knob.inputType === "lines") {
    input = el("textarea", { id, class: "field", rows: 3, disabled: configSaveBusy, "aria-label": knob.label,
      oninput: (event) => setConfigDraft(knob, [...knob.requiredValues, ...event.target.value.split("\n").map((line) => line.trim()).filter((line) => line && !knob.requiredValues.includes(line))]) });
    input.value = Array.isArray(value) ? value.filter((line) => !knob.requiredValues.includes(line)).join("\n") : value;
  } else {
    input = el("input", { id, class: "field", type: "number", min: knob.inputMin, max: knob.inputMax, step: knob.inputStep, required: true,
      disabled: configSaveBusy, value: configInputValue(knob, value), "aria-label": knob.label,
      oninput: (event) => setConfigDraft(knob, event.target.value === "" ? null : configStoredValue(knob, Number(event.target.value))) });
  }
  return el("div", { class: `knob ${knob.operatorSet ? "operator" : "defaulted"}`, testid: "config-knob", data: { key: knob.key } },
    el("div", {}, el(knob.inputType === "patterns" ? "span" : "label", { for: knob.inputType === "patterns" ? null : id, text: knob.label }), knob.showKey ? el("div", { class: "key", text: knob.key }) : null),
    el("div", { class: "config-control" }, input, el("p", { class: "system-helper", text: knob.description })),
    el("span", { class: `tag ${knob.operatorSet ? "tone-teal" : ""}`, testid: "knob-layer", text: knob.layer }),
  );
}

$("configs-save").addEventListener("click", async () => {
  if (configSaveBusy || configDrafts.size === 0) return;
  const invalid = configGroups(configFamilies).flatMap(group => group.knobs).find(knob => {
    const draft = configDrafts.get(knob.key);
    return draft && knob.inputType === "integer" && (!Number.isInteger(draft.value)
      || (knob.min != null && draft.value < knob.min) || (knob.max != null && draft.value > knob.max));
  });
  if (invalid) {
    $("configs-search").value = "";
    renderConfigs();
    const input = $("config-input-" + invalid.key);
    input.focus();
    input.reportValidity();
    return;
  }
  if (!Array.from($("configs-knobs").querySelectorAll("input")).every((input) => input.reportValidity())) return;
  configSaveBusy = true;
  configGeneration += 1;
  renderConfigs();
  $("configs-save-status").textContent = "Please wait while Culler saves the changes.";
  try {
    const result = await api("/api/config", { method: "POST", body: JSON.stringify({ changes: Array.from(configDrafts.values()) }) });
    configFamilies = result.families ?? [];
    configDrafts.clear();
    $("configs-save-status").textContent = "Culler saved the changes. Each setting states when it takes effect.";
  } catch (error) {
    $("configs-save-status").textContent = error.message;
  } finally {
    configSaveBusy = false;
    configGeneration += 1;
    renderConfigs();
  }
});
$("configs-discard").addEventListener("click", () => {
  configDrafts.clear();
  $("configs-save-status").textContent = "You discarded the unsaved changes.";
  renderConfigs();
});

let configFamilies = [];
let routingRoutes = [];
let configRole = null;
const configRoleDialog = createRoleDialog(() => {
  configRole = null;
  renderConfigRoute();
}, () => document.querySelector(`[data-testid="config-route-${configRole}"]`));
function renderConfigRoute() {
  const node = flowmapView(topologyView([], [], routingRoutes, Date.now(), state.settings), []).nodes.find((row) => row.role === configRole);
  if (node) updateRoleDialog(configRoleDialog, flowSideNode(node, () => configRoleDialog.close(), {
    bindings: flow.bindings, busy: flow.pickBusy, error: flow.pickError, onRoute: setRoute, onManage: (role, entry) => agentProfiles.open(role, false, entry), onCreate: (role, entry) => agentProfiles.open(role, true, entry),
  }));
  else if (configRoleDialog.open) configRoleDialog.close();
  for (const button of document.querySelectorAll('#configs-routing [data-testid^="config-route-"]')) {
    button.setAttribute("aria-expanded", String(button.dataset.testid === `config-route-${configRole}`));
  }
}
let projectList = [];
let configsAvailable = false;
const projectDeletion = createProjectDeletion({
  api,
  async deleted() {
    await refreshPipelinePause(true);
    await refreshConfigs();
    await tick();
  },
});

function renderConfigs() {
  $("configs-draft-indicator").hidden = configDrafts.size === 0;
  if (!configsAvailable) return;
  renderConfigRoute();
  const groups = configGroups(configFamilies, $("configs-search").value);
  $("configs-save").disabled = configSaveBusy || configDrafts.size === 0;
  $("configs-discard").disabled = configSaveBusy || configDrafts.size === 0;
  if (configSaveBusy || !$("configs-knobs").contains(document.activeElement)) $("configs-knobs").replaceChildren(
    ...groups.map((group) =>
      el(
        "div",
        { class: "knob-family", testid: `config-family-${group.family}` },
        el(
          "div",
          { class: "subhead" },
          document.createTextNode(group.label),
          el("small", { text: `${group.knobs.length} ${group.knobs.length === 1 ? "setting" : "settings"} · ${group.operatorCount} operator-set` }),
        ),
        ...group.knobs.map(knobNode),
      ),
    ),
    ...(groups.length ? [] : [el("p", { class: "empty", text: "No setting matches that filter." })]),
  );

  fill(
    $("configs-routing"),
    routingRoutes.length === 0
      ? []
      : [
          el(
            "table",
            { class: "table" },
            el("thead", {}, el("tr", {}, el("th", { text: "Role" }), el("th", { text: "Harness" }), el("th", { text: "Agent profile" }), el("th", { text: "Model" }))),
            el(
              "tbody",
              {},
              routingRows(routingRoutes).map((row) =>
                el(
                  "tr",
                  { testid: "routing-row", data: { role: row.role } },
                  el("td", {}, el("button", { type: "button", class: "btn small", text: row.label, testid: `config-route-${row.role}`, "aria-haspopup": "dialog", "aria-expanded": String(configRole === row.role), onclick: () => { configRole = row.role; renderConfigRoute(); } }), row.collie ? tag("outside the pipeline") : null),
                  el("td", { class: "mono", data: { label: "Harness" }, text: row.harness }),
                  el("td", { class: "mono", data: { label: "Agent profile" }, text: row.profileName }),
                  el("td", { class: "mono", data: { label: "Model" }, text: row.model || "—" }),
                ),
              ),
            ),
          ),
        ],
    "No routing table has been read.",
  );

  fill(
    $("configs-projects"),
    projectList.length === 0
      ? []
      : [
          el(
            "table",
            { class: "table" },
            el("thead", {}, el("tr", {}, el("th", { text: "Project" }), el("th", { text: "Sandbox profile" }), el("th", { text: "Pipeline" }), el("th", { text: "Actions" }))),
            el(
              "tbody",
              {},
              projectRows(projectList).map((row) =>
                el(
                  "tr",
                  { testid: "project-row" },
                  el(
                    "td",
                    {},
                    el("div", { text: projectName(row.id, projectList) }),
                    el("button", { type: "button", class: "btn small", text: "Environment", testid: `environment-project-${row.id}`, onclick: () => openPage("environment", [row.id]) }),
                    el("button", { type: "button", class: "btn small", text: "Role customization", testid: `customize-project-${row.id}`, onclick: () => roleCustomization.open(row.id) }),
                    el("span", { class: `tag ${row.live ? "tone-leaf" : "tone-stone"}`, text: row.status }),
                  ),
                  el("td", { class: "mono", data: { label: "Sandbox profile" }, text: row.profile }),
                  el("td", {}, state.pause ? projectPauseNode(state.pause.projects.find((ws) => ws.id === row.id) ?? row) : "Pipeline state unavailable"),
                  el("td", { class: "project-delete-cell", data: { label: "Actions" } }, projectDeletion.button({ id: row.id, title: projectName(row.id, projectList) })),
                ),
              ),
            ),
          ),
        ],
    "No projects registered.",
  );
}

$("configs-search").addEventListener("input", renderConfigs);
$("configs-knobs").addEventListener("focusin", event => {
  const control = event.target;
  requestAnimationFrame(() => {
    if (!control.isConnected || document.activeElement !== control) return;
    const bottom = control.getBoundingClientRect().bottom;
    const top = document.querySelector(".config-savebar").getBoundingClientRect().top;
    if (bottom > top - 16) window.scrollBy(0, bottom - top + 16);
  });
});

async function refreshConfigs() {
  await settleRegions([
    discordPanel.refresh(),
    (async () => {
      const generation = configGeneration;
      const [config, routing, projects] = await Promise.all([
        api("/api/config"), api("/api/routing"), api("/api/projects"),
      ]);
      flow.bindings = routing.bindings ?? [];
      if (generation === configGeneration && !configSaveBusy) configFamilies = config.families ?? [];
      routingRoutes = routing.routes ?? [];
      projectList = projects.projects ?? [];
      configsAvailable = true;
      renderConfigs();
    })(),
    readRegion("pipeline-outbox", async () => renderLag(outboxLagView((await api("/api/outbox/lag")).surfaces ?? []))),
    readRegion("resident-status", async () => renderHealth(healthView(await api("/api/health")))),
  ]);
  renderDoctor();
}

// ------------------------------------------------------------------ 7. Logs

/** The level facet chips, rebuilt with the stats: counts over the whole
 * window, toggles narrowing the view. Offered only when the window carries
 * records — a prose stream has no levels to facet on. */
function levelChipNodes(view) {
  const counts = levelCounts(view);
  if (counts === null) return [];
  return LOG_LEVELS.map((level) => {
    const on = state.logsLevels.has(level);
    return el("button", {
      type: "button",
      class: `lvlchip lvl-${level}${on ? " on" : ""}`,
      testid: `logs-level-${level}`,
      title: on ? `Hide ${level} records` : `Show ${level} records`,
      text: `${level[0].toUpperCase() + level.slice(1)} ${counts[level]}`,
      "aria-pressed": String(on),
      onclick: () => {
        if (on) state.logsLevels.delete(level);
        else state.logsLevels.add(level);
        if (state.logsLastRaw) renderLogs(state.logsLastRaw);
      },
    });
  });
}

let logsRequestSerial = 0;
let logsOlderBusy = false;
let logsRenderedSource = null;

function renderLogs(res, { older = false } = {}) {
  const runID = state.logsUnit === RUN_LANE && res.run_id === state.logsRun ? state.logsRun : null;
  const failure = [...(res.records ?? [])].reverse().find(record => record?.level === "error" || record?.level === "warn");
  setChildren($("logs-collie"), runID ? diagnosticButton({
    title: "Run diagnostics", run_id: runID,
    summary: failure?.condition || failure?.error?.message || failure?.msg || "Inspect the recorded result and trace of this run.",
    recorded_at: failure?.ts,
  }, askDiagnostic, "run-ask-collie") : null);
  const view = logsView(res, { grep: state.logsGrep, levels: [...state.logsLevels] });
  state.logsLast = view;
  const pane = $("logs-lines");
  pane.classList.toggle("wrap", $("logs-wrap").checked);
  setChildren($("logs-levels"), levelChipNodes(view));
  $("logs-matches").textContent = state.logsGrep.trim() ? `${view.shown} ${view.shown === 1 ? "match" : "matches"}` : "";
  $("logs-limit").value = String(state.logsTail);
  const source = logsHash({ unit: state.logsUnit, stream: state.logsStream, run: state.logsRun });
  const sameSource = logsRenderedSource === source;
  const oldRows = [...pane.querySelectorAll(".logline")];
  const paneTop = pane.getBoundingClientRect().top;
  const anchor = sameSource ? oldRows.find((row) => row.getBoundingClientRect().bottom > paneTop) : null;
  const anchorText = anchor?.querySelector(".t")?.textContent;
  const anchorTop = anchor?.getBoundingClientRect().top;

  const atBottom = pane.scrollHeight - pane.scrollTop - pane.clientHeight < 40;
  const nodes = view.lines.map((line) =>
    el(
      "div",
      { class: `logline${line.match ? " match" : ""}${line.level ? ` lvl-${line.level}` : ""}`, testid: "log-line", data: { key: `${source}:${line.text}` } },
      el("span", { class: "n", text: String(line.n) }),
      el("span", { class: "t", text: line.text }),
    ),
  );
  if (view.note !== "") nodes.unshift(el("div", { class: "note", testid: "logs-note", text: view.note }));
  setChildren(pane, ...nodes);
  // A tail is read at its end; only follow it when the operator already is.
  if (!sameSource || (atBottom && !older)) {
    pane.scrollTop = pane.scrollHeight;
  } else if (anchor) {
    const replacement = [...pane.querySelectorAll(".logline")].find((row) => row.querySelector(".t")?.textContent === anchorText);
    if (replacement) pane.scrollTop += replacement.getBoundingClientRect().top - anchorTop;
  }
  logsRenderedSource = source;
}

function renderLogSources(logRuns, runs) {
  const selectSource = (unit, run = null) => {
    state.logsUnit = unit;
    if (unit !== RUN_LANE) {
      state.logsProcess = unit;
      state.logsProcessRecords = [];
      setChildren($("logs-recent"));
      $("logs-run-count").textContent = "The process logs load here.";
    }
    state.logsRun = run;
    state.logsStream = "events";
    state.logsTail = TAIL_DEFAULT;
    $("logs-unit").value = unit;
    fillLogStreamPicker();
    history.replaceState(null, "", logsHash({ unit, stream: "events", run }));
    tick();
  };
  const sourceButton = (name, status, selected, testid, onclick) => el("button", {
    type: "button", class: `logs-source-row${selected ? " selected" : ""}`, testid,
    "aria-pressed": String(selected), title: name, onclick,
  }, el("span", { class: "logs-name", text: name }), el("span", { class: "note", text: status }));
  setChildren($("logs-processes"), ...UNIT_OPTIONS.filter((unit) => unit !== RUN_LANE).map((unit) =>
    sourceButton(unit === "mc" ? "mc (command)" : unit, unit === "mc" ? "on demand" : "tail",
      state.logsProcess === unit, `logs-process-${unit}`, () => selectSource(unit))));
  const recent = processRuns(state.logsProcessRecords, runs, logRuns, state.pause?.projects);
  setChildren($("logs-recent"), ...recent.map((run) =>
    sourceButton(run.label, run.status, state.logsUnit === RUN_LANE && state.logsRun === run.id,
      `logs-recent-${run.id}`, () => selectSource(RUN_LANE, run.id))));
  $("logs-run-count").textContent = recent.length
    ? `${recent.length} ${recent.length === 1 ? "run" : "runs"} in ${state.logsProcess} logs`
    : `No run references in these ${state.logsProcess} logs.`;

}

async function refreshLogs({ older = false } = {}) {
  if (logsOlderBusy && !older && state.logsTail === logsOlderBusy.tail
    && logsHash({ unit: state.logsUnit, stream: state.logsStream, run: state.logsRun }) === logsOlderBusy.selection) return false;
  const serial = ++logsRequestSerial;
  let logRuns = null;
  let spineRuns = null;
  const lists = Promise.all([api("/api/logs/runs").catch(() => null), api("/api/runs").catch(() => null)]).then(results => {
    if (serial !== logsRequestSerial) return;
    [logRuns, spineRuns] = results;
    state.logsRuns = runOptions(logRuns?.runs ?? [], spineRuns?.runs ?? []);
    if (state.logsUnit === RUN_LANE && state.logsRun === null && state.logsRuns.length > 0) state.logsRun = state.logsRuns[0].id;
    fillLogRunPicker();
    renderLogSources(logRuns?.runs ?? [], spineRuns?.runs ?? []);
    if (!logRuns || !spineRuns) $("logs-run-count").textContent += " Run details are unavailable.";
  });
  if (state.logsUnit === RUN_LANE && state.logsRun === null) await lists;
  if (serial !== logsRequestSerial) return false;

  if (state.logsUnit === RUN_LANE) history.replaceState(null, "", logsHash({ unit: RUN_LANE, run: state.logsRun }));
  const query = logsQuery({
    unit: state.logsUnit,
    stream: state.logsStream,
    tail: state.logsTail,
    run: state.logsRun,
  });
  if (query === null) {
    state.logsLastRaw = { run_id: state.logsRun ?? "unknown", exists: false, lines: [] };
    renderLogs(state.logsLastRaw);
    setChildren($("logs-lines"), el("div", { class: "note", testid: "logs-note", text: "No run diagnostics exist yet." }));
    return;
  }
  const res = await api(query);
  if (serial !== logsRequestSerial || query !== logsQuery({ unit: state.logsUnit, stream: state.logsStream, tail: state.logsTail, run: state.logsRun })) return false;
  if (state.logsUnit !== RUN_LANE) {
    state.logsProcess = state.logsUnit;
    state.logsProcessRecords = res.records ?? [];
  }
  if (logRuns && spineRuns) renderLogSources(logRuns.runs ?? [], spineRuns.runs ?? []);
  state.logsLastRaw = res;
  renderLogs(res, { older });
  return true;
}

function fillLogStreamPicker() {
  const streams = streamsFor(state.logsUnit);
  if (!streams.includes(state.logsStream)) state.logsStream = streams[0];
}

function fillLogRunPicker() {
  const select = $("logs-run");
  if (state.logsRuns.length === 0) {
    setChildren(select, el("option", { value: "", text: "no runs with diagnostics" }));
    select.value = "";
    return;
  }
  setChildren(select, ...state.logsRuns.map((run) => el("option", { value: run.id, text: run.label })));
  if (state.logsRun !== null && state.logsRuns.some((r) => r.id === state.logsRun)) {
    select.value = state.logsRun;
  }
}

{
  const select = $("logs-unit");
  setChildren(select,
    ...UNIT_OPTIONS.map((value) =>
      el("option", { value, text: value === RUN_LANE ? "run diagnostics" : value }),
    ),
  );
  select.addEventListener("change", () => {
    state.logsUnit = select.value;
    state.logsTail = TAIL_DEFAULT;
    fillLogStreamPicker();
    history.replaceState(null, "", logsHash({ unit: state.logsUnit, stream: state.logsStream, run: state.logsRun }));
    tick();
  });
}

$("logs-run").addEventListener("change", () => {
  state.logsRun = $("logs-run").value || null;
  state.logsTail = TAIL_DEFAULT;
  history.replaceState(null, "", logsHash({ unit: RUN_LANE, run: state.logsRun }));
  tick();
});
fillLogStreamPicker();

$("logs-limit").addEventListener("change", () => {
  state.logsTail = Number($("logs-limit").value);
  tick();
});

$("logs-grep").addEventListener("input", () => {
  state.logsGrep = $("logs-grep").value;
  // The grep is browser-side over the window already fetched: interpretation
  // belongs here, and re-asking the server for the same bytes to filter them
  // would spawn an mc process per keystroke.
  if (state.logsLastRaw) renderLogs(state.logsLastRaw);
});

$("logs-wrap").addEventListener("change", () => {
  $("logs-lines").classList.toggle("wrap", $("logs-wrap").checked);
});

async function loadOlderLogs() {
  const pane = $("logs-lines");
  const selection = logsHash({ unit: state.logsUnit, stream: state.logsStream, run: state.logsRun });
  if (state.tab !== "logs" || logsOlderBusy || pane.scrollTop > 1 || logsRenderedSource !== selection
    || !state.logsLastRaw?.truncated || !canLoadOlder(state.logsTail)) return;
  const previousTail = state.logsTail;
  const requestedTail = nextTail(previousTail);
  logsOlderBusy = { selection, tail: requestedTail };
  state.logsTail = requestedTail;
  pane.setAttribute("aria-busy", "true");
  try {
    if (await refreshLogs({ older: true })) clearError();
  } catch (err) {
    if (selection === logsHash({ unit: state.logsUnit, stream: state.logsStream, run: state.logsRun })) {
      if (state.logsTail === requestedTail) state.logsTail = previousTail;
      showError(err, { read: true });
    }
  } finally {
    logsOlderBusy = false;
    pane.removeAttribute("aria-busy");
  }
}

$("logs-lines").addEventListener("scroll", loadOlderLogs);
$("logs-lines").addEventListener("wheel", (event) => {
  if (event.deltaY < 0) loadOlderLogs();
}, { passive: true });

$("logs-copy").addEventListener("click", async () => {
  const button = $("logs-copy");
  try {
    await navigator.clipboard.writeText(copyText(state.logsLast));
    button.textContent = "Copied";
  } catch {
    // A clipboard the browser refuses is not an error worth a banner; the
    // lines are on screen and selectable either way.
    button.textContent = "Select the lines to copy";
  }
  setTimeout(() => {
    button.textContent = "Copy visible";
  }, 2000);
});

// -------------------------------------------------------------- the shell

/** Records the lease rows (`{leases: [...]}` from /api/leases) and the
 * settings row together — the badge reads both. */
function setLeases(leases, settings) {
  state.leases = leases?.leases ?? [];
  state.settings = settings ?? null;
  state.leaseReadAt = Date.now();
  state.leaseError = "";
  state.dockerUnavailable = false;
  $("docker-error").hidden = true;
  $("docker-error-disclosure").open = false;
  renderHeader();
  renderProjectContext();
}

/** Opens a page and its optional section or record. */
function openPage(page, rest = [], refresh = true) {
  const button = document.querySelector(`nav .tab[data-page="${page}"]`);
  if (!button && page !== "vision" && page !== "environment") return;
  if (page !== "review") {
    state.readerWanted = null;
    state.readerAuto = false;
    hideReader(false);
  }
  // The Review tab keeps the open packet. Otherwise a Review address without a
  // packet opens the first packet of the list.
  const keepPacket = page === "review" && rest.length === 0 && state.tab === "review" && state.selectedPacket !== null;
  if (page === "review" && rest.length === 0 && !keepPacket) state.readerAuto = true;
  if (state.tab === "vision" && page !== "vision") vision.leave();
  if (state.tab === "environment" && page !== "environment") environmentPage.leave();
  if (state.tab === "collie" && page !== "collie") collieModel.dispose();
  $("vision-pane").hidden = page !== "vision";
  $("environment-pane").hidden = page !== "environment";
  if (page === "environment") {
    window.scrollTo(0, 0); environmentRead = environmentPage.open(rest[0]);
  }
  if (page === "vision") {
    window.scrollTo(0, 0); visionRead = vision.open(rest[0]);
  }
  ++state.scopeSerial;
  state.tab = page;
  updateBoardCommandBar();
  if (flow.view) paintFlowmap();
  renderConfigRoute();
  renderProjectContext();
  if (page === "collie") state.collieOpened = true;
  for (const other of document.querySelectorAll("nav .tab[data-panel]")) {
    const on = other === button;
    other.classList.toggle("active", on);
    other.setAttribute("aria-current", on ? "page" : "false");
    $(other.dataset.panel).hidden = !on;
  }
  centerActiveTab();
  const hash =
    keepPacket ? packetHash(state.selectedPacket)
      : page === "logs" && rest.length > 0
        ? logsHash({ unit: rest[0], stream: rest[1], run: rest[0] === RUN_LANE ? rest[1] : null })
        : routeHash(page, rest);
  if (location.hash !== hash) history.replaceState(null, "", hash);
  clearError();
  renderHeader();
  if (page === "configs") { selectConfigSection(rest[0]); deploymentCheck.wake(); }
  if (refresh) tick();
}

for (const button of document.querySelectorAll("nav .tab[data-panel]")) {
  button.addEventListener("click", () => openPage(button.dataset.page, button.dataset.page === "configs" ? [configSection] : []));
}

/** Applies the address bar to the shell: a reload lands where the operator
 * was, and Pipeline's deep-link lands on the right tail. */
function applyRoute(refresh = true) {
  const route = parseRoute(location.hash);
  if (route === null) return;
  if (route.page === "logs") {
    const picked = parseLogsHash(location.hash);
    if (picked) {
      state.logsUnit = picked.unit;
      state.logsStream = picked.stream;
      if (picked.run !== null) state.logsRun = picked.run;
      state.logsTail = TAIL_DEFAULT;
      $("logs-unit").value = picked.unit;
      fillLogStreamPicker();
    }
  }
  if (route.page === "vision" || route.page === "environment" || route.page !== state.tab || /^#\/?activity(?:\/|$)/.test(location.hash)) openPage(route.page, route.rest, refresh);
  else if (route.page === "configs") selectConfigSection(route.rest[0], true);
  else if (route.page === "logs" && refresh) tick();
  if (route.page === "review") followReaderRoute();
}

window.addEventListener("hashchange", () => applyRoute());
/** A tab row narrower than its tabs keeps the selected page in view. */
function centerActiveTab() {
  const button = document.querySelector("nav.seg .tab.active");
  const tabs = button?.closest("nav.seg");
  if (tabs && tabs.scrollWidth > tabs.clientWidth) {
    tabs.scrollLeft = Math.max(0, button.offsetLeft - (tabs.clientWidth - button.offsetWidth) / 2);
  }
}
window.addEventListener("resize", centerActiveTab, { passive: true });

// The pinned notice folds to its title line once the page scrolls. The fold
// changes the page height, and the browser then moves the scroll position by
// that height. A folded notice thus opens again only at the top of the page.
let noticeFolded = false;
window.addEventListener("scroll", () => {
  const folded = window.scrollY > (noticeFolded ? 0 : 48);
  if (folded === noticeFolded) return;
  noticeFolded = folded;
  $("system-notices").classList.toggle("folded", folded);
}, { passive: true });

/** The poll cadence: tight only while the Collie view shows an active session
 * that owes a reply. */
function pollMs() {
  return state.tab === "vision" || (state.tab === "collie" && state.pollTight) ? TIGHT_MS : LAZY_MS;
}

function schedule() {
  clearTimeout(state.timer);
  if (!document.hidden) state.timer = setTimeout(tick, pollMs());
}

const REFRESHERS = {
  board: refreshBoard,
  collie: refreshCollie,
  vision: async () => {
    if (!visionRead) return vision.refresh();
    const read = visionRead;
    await read;
    if (visionRead === read) visionRead = null;
  },
  environment: async () => {
    if (!environmentRead) return environmentPage.refresh();
    const read = environmentRead;
    await read;
    if (environmentRead === read) environmentRead = null;
  },
  review: refreshReview,
  pipeline: refreshPipeline,
  configs: refreshConfigs,
  logs: refreshLogs,
};

async function refreshLease() {
  try {
    const [leases, settings] = await Promise.all([api("/api/leases"), api("/api/settings")]);
    setLeases(leases, settings);
  } catch (err) {
    state.leaseError = err.message;
    renderHeader();
    throw err;
  }
}

const pageReads = new Map();
const pageRegions = { vision: "vision-pane", environment: "environment-pane", board: "board-columns", collie: "session-list", review: "packets", pipeline: "pipeline-topology", configs: "configs-content", logs: "logs-lines" };
const regionReads = new Map();
async function readRegion(id, read) {
  const host = $(id);
  if (!host) return read();
  const serial = (regionReads.get(id) ?? 0) + 1;
  regionReads.set(id, serial);
  host.setAttribute("aria-busy", "true");
  host.dataset.requestState ??= "pending";
  let notice = document.getElementById(`${id}-request`);
  if (!notice) {
    notice = el("div", { id: `${id}-request`, class: "request-status", role: "status", hidden: true });
    host.before(notice);
  }
  try {
    const result = await read();
    if (serial === regionReads.get(id)) { notice.hidden = true; host.dataset.requestState = "ready"; }
    return result;
  } catch (error) {
    if (serial === regionReads.get(id)) {
      const retained = id !== "activity-events" && (host.dataset.requestState === "ready" || host.dataset.requestState === "stale");
      host.dataset.requestState = retained ? "stale" : "failed";
      const placeholder = host.children.length === 1 && host.firstElementChild.classList.contains("loading");
      if (placeholder) setChildren(host, emptyBox("Not loaded.", false, "failed"));
      setChildren(notice, el("span", { text: `${retained ? "Last known data. " : ""}${error.message}` }),
        el("button", { class: "btn small", type: "button", text: "Retry", onclick: () => { if (id === "session-list") state.collieOpened = true; tick(); } }));
      notice.hidden = false;
    }
    throw error;
  } finally {
    if (serial === regionReads.get(id)) host.setAttribute("aria-busy", "false");
  }
}
async function settleRegions(reads) {
  const results = await Promise.allSettled(reads);
  const failure = results.find(result => result.status === "rejected");
  if (failure) throw failure.reason;
}
function tick() {
  if (document.hidden || document.body.classList.contains("access-denied")) return Promise.resolve();
  const scope = state.scopeSerial;
  const tab = state.tab;
  const key = `${tab}:${scope}:${state.selected}:${state.logsUnit}:${state.logsStream}:${state.logsRun}:${state.logsTail}:${state.activityKind}:${state.activityRevision}`;
  if (pageReads.has(key)) return pageReads.get(key);
  const errorsBefore = errorSerial;
  const read = (async () => {
    try {
      await settleRegions([
        refreshPipelinePause(), readRegion("lease-badge", refreshLease),
        readRegion(pageRegions[tab], REFRESHERS[tab] ?? refreshBoard),
        readRegion("project-vision", refreshVisionNotice),
      ]);
      if (scope === state.scopeSerial && errorSerial === errorsBefore && errorFromRead) clearError();
    } catch (error) {
      if (scope === state.scopeSerial) showError(error, { read: true });
    } finally {
      pageReads.delete(key);
      if (scope === state.scopeSerial || (tab === state.tab && !pageReads.size)) {
        renderHeader(); schedule();
      }
    }
  })();
  pageReads.set(key, read);
  return read;
}
document.addEventListener("visibilitychange", () => {
  clearTimeout(state.timer);
  updateBoardCommandBar();
  if (!document.hidden) tick();
});
const prefetchPages = {
  board: ["/api/tasks", "/api/leases", "/api/settings", "/api/projects", "/api/packets"],
  review: ["/api/packets", "/api/leases", "/api/settings"],
  pipeline: ["/api/tasks", "/api/leases", "/api/settings", "/api/packets", "/api/runs", "/api/routing", "/api/wiki-states"],
  collie: ["/api/sessions"],
  configs: ["/api/config", "/api/routing", "/api/projects"],
  logs: ["/api/logs/runs", "/api/runs"],
};
for (const button of document.querySelectorAll("nav .tab[data-page]")) {
  const prepare = () => {
    const paths = button.dataset.page === "board" && emptyProjectRegistry()
      ? ["/api/pause", "/api/projects/readiness"] : prefetchPages[button.dataset.page] ?? [];
    if (!pageReads.size) void requests.prefetch(paths, () => !document.hidden);
  };
  button.addEventListener("pointerenter", prepare);
  button.addEventListener("focus", prepare);
}

// ------------------------------------------------------- write affordances

$("resume").addEventListener("click", async () => {
  if (!state.selected) return;
  try {
    await resumeSession(state.selected);
    clearError();
    tick();
  } catch (err) {
    showError(err);
  }
});

let collieModelRequest = null;
async function switchCollieModel(binding, retry = false, expected) {
  if (state.collieSessionBusy || state.sendInFlight || state.collieReplyBusy || !state.selected) {
    throw new Error("Wait for the current conversation operation to finish.");
  }
  if (!collieModelRequest || collieModelRequest.binding !== binding ||
      (!retry && ![collieModelRequest.previous, collieModelRequest.result?.session_id].includes(state.selected))) {
    collieModelRequest = { binding, previous: state.selected, request_id: crypto.randomUUID(), empty: !state.collieHasMessages };
  }
  const operation = collieModelRequest;
  state.collieSessionBusy = true;
  state.collieRevision++;
  renderCollieControls();
  try {
    const result = operation.result ?? await api(`/api/sessions/${operation.previous}/model`, {
      method: "POST", body: JSON.stringify({ binding, request_id: operation.request_id }),
    });
    operation.result = result;
    if (result.session_id !== operation.previous && result.status !== "active") {
      try { await resumeSession(result.session_id); result.status = "active"; }
      catch (err) { throw new Error(`The conversation exists. Culler could not prepare it. ${err.message} Select Retry.`); }
    }
    const existing = state.sessions.find(row => row.id === result.session_id);
    if (state.selected !== result.session_id) {
      state.selected = result.session_id;
      state.collieHistorySession = null;
      state.collieHasMessages = false;
      state.collieReplyBusy = false;
      $("collie-transcript").replaceChildren(collieEmpty());
    }
    state.sessions = state.sessions.filter(row => row.id !== result.replaced && row.id !== result.session_id);
    state.sessions.push({ ...existing, id: result.session_id, status: result.status, binding: result.binding });
    try { await refreshTranscript(); }
    catch (err) { throw new Error(`The model change succeeded. Culler could not load the conversation. ${err.message} Select Retry.`); }
    let routing;
    try {
      routing = await api("/api/routing", { method: "POST", body: JSON.stringify({ role: "collie", binding, expected }) });
    } catch (err) {
      throw new Error(`The conversation uses the selected model. Culler could not set the default model. ${err.message} Select Retry.`);
    }
    collieModelRequest = null;
    clearError();
    return { routing, notice: result.preserved && operation.empty
      ? "Another message reached the previous conversation. Culler kept that conversation and opened a new conversation." : "" };
  } catch (err) {
    if (!operation.result) throw new Error(`${err.message} Your draft remains available. Select Retry to check this model change.`);
    throw err;
  } finally {
    state.collieRevision++;
    state.collieSessionBusy = false;
    renderCollieControls();
  }
}

const openDiagnostic = createDiagnosticChat({ api, openConversation: async session => {
  state.collieRevision++;
  state.selected = session.session_id;
  if (!state.sessions.some(row => row.id === session.session_id)) {
    state.sessions.push({ id: session.session_id, status: session.status, binding: session.binding });
  }
  state.collieHistorySession = null;
  state.collieHasMessages = true;
  state.collieReplyBusy = true;
  state.pollTight = true;
  setChildren($("collie-transcript"), el("div", { class: "loading", text: "Culler reads the conversation…" }));
  renderSessions();
  for (const dialog of document.querySelectorAll("dialog[open]")) dialog.close();
  openPage("collie");
  await readRegion("collie-transcript", refreshTranscript).catch(showError);
} });

async function askDiagnostic(context) {
  if (state.sendInFlight || state.collieSessionBusy) throw new Error("Wait for the current conversation request to complete.");
  state.collieSessionBusy = true;
  renderCollieControls();
  try { await openDiagnostic(context); }
  finally {
    state.collieSessionBusy = false;
    renderCollieControls();
    if (state.tab === "collie") { void tick(); $("send-body").focus(); }
  }
}

function conditionCollieButton(condition) {
  return diagnosticButton(conditionDiagnostic(condition, systemView.unavailable), askDiagnostic);
}

async function newCollieSession(ensure = false) {
  if (state.collieSessionBusy || state.sendInFlight) return false;
  state.collieSessionBusy = true;
  renderCollieControls();
  try {
    const res = await api("/api/sessions", { method: "POST", body: JSON.stringify(ensure ? { ensure: true } : {}) });
    state.collieRevision++;
    state.selected = res.session_id;
    state.collieHistorySession = res.session_id;
    state.collieHasMessages = false;
    state.collieReplyBusy = false;
    const row = { id: res.session_id, status: res.status, binding: res.binding };
    state.sessions.push(row);
    if (ensure && row.status !== "active") {
      await resumeSession(row.id);
      row.status = "active";
    }
    $("collie-transcript").replaceChildren(collieEmpty());
    clearError();
    await refreshTranscript();
    return true;
  } catch (err) {
    showError(err);
    return false;
  } finally {
    state.collieSessionBusy = false;
    renderCollieControls();
  }
}
$("new-session").addEventListener("click", () => newCollieSession());

/** The transcript shows the sent message and a pending Collie row before the
 * send reaches the spine. The next history read renders the same rows from
 * the record, or drops them when the send failed. */
function showSentTurn(body) {
  const pane = $("collie-transcript");
  const empty = pane.querySelector(".empty, .conversation-empty");
  if (empty) empty.remove();
  pane.append(
    messageNode({ ...provisionalRow(body), direction: "send", who: "You", own: true, provisional: false, age: "just now" }),
    messageNode(provisionalRow("")),
  );
  pane.scrollTop = pane.scrollHeight;
}

$("send-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const input = $("send-body");
  const originalText = input.value;
  const body = originalText.trim();
  if ((!body && !ordinaryUploads.count()) || !state.selected || input.disabled || state.sendInFlight) return;
  const sessionID = state.selected;
  let uploadID;
  input.value = "";
  ordinaryComposer.resize();
  showSentTurn(body);
  // The send lands on an active session, so a reply is owed. The next history
  // read that shows the reply returns the loop to the lazy cadence.
  state.pollTight = true;
  state.sendInFlight = true;
  renderCollieControls();
  try {
    const files = await ordinaryUploads.files();
    uploadID = ordinaryUploads.requestID(body);
    await api(`/api/sessions/${sessionID}/${files.length ? "upload" : "send"}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(files.length ? { body, files, request_id: uploadID } : { body }),
    });
    if (state.selected === sessionID) { state.collieHistorySession = sessionID; state.collieHasMessages = true; }
    const session = state.sessions.find(row => row.id === sessionID);
    if (session && session.first_message == null) session.first_message = body;
    state.collieReplyBusy = true;
    renderSessions();
    ordinaryUploads.clear();
    clearError();
  } catch (err) {
    input.value = originalText;
    ordinaryComposer.resize();
    showError(err);
  } finally {
    state.sendInFlight = false;
    renderCollieControls();
  }
  tick();
});

// ------------------------------------------------------------------- boot

// The Review tab's badge is the one number an operator wants without opening a
// page, so it is read once at boot rather than only when a packet-reading page
// happens to be visited.
api("/api/packets")
  .then((res) => { state.packets = res.packets ?? []; if (state.projectReady) renderReviewCount(queueStats(ownRows(state.packets), state.settings).awaiting); })
  .catch(() => {});
renderHeader();
renderBoard();
renderReview();
renderTopology(pipelineFlowmap([], Date.now()), []);
applyRoute(false);
document.documentElement.removeAttribute("data-initial-page");
tick();
// Every listener above is attached: a click delivered before this mark is
// lost, so automated drivers (the smokes) wait for it before interacting.
document.body.dataset.ready = "1";
