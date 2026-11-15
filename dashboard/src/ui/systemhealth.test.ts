import { expect, test } from "bun:test";
import { systemIssues, acceptSystemStatus, actionControl, conditionTitle, createSystemStatus, createDeploymentCheck, connectionGroups, connectionDisclosureLabel, DEPLOYMENT_CHECK_MS, diagnosticResults, refreshSummary, conditionExplanation, conditionRetry, conditionSummary, conditionImpact, recoveryText, releaseLabel, releaseView, repairText } from "./systemhealth.js";

test("one shared decision represents its task blocks and preserves the full question", () => {
  const decision = { id: "proposal-decision:4", project_id: "project", proposal_decision_id: 4,
    condition: "Which checks apply?\nKeep all evidence in this question.",
    affected_tasks: [7, 8].map(task_id => ({ task_id, title: `Proposal ${task_id}`, owns_block: true })) };
  const tasks = [7, 8].map(task_id => ({ id: `task:${task_id}`, project_id: "project", task_id, blocked_kind: "proposal", block_decision_id: 4 }));
  const independent = { id: "task:5", project_id: "project", task_id: 5, blocked_kind: "report" };
  const conditions = [decision, independent, ...tasks];
  expect(systemIssues(conditions)).toEqual([decision, independent]);
  expect(systemIssues(conditions)[0]).toBe(decision);
  expect(conditions).toHaveLength(4);
  expect(systemIssues(tasks)).toEqual(tasks);
  expect(systemIssues()).toEqual([]);
});

test("a connection that no role uses is not an issue", () => {
  const unused = { id: "connection:spare:connection_access:", group: "connections", state: "stopped", roles: [], diagnostic_id: 3 };
  const unusedProfile = { id: "connection:main:model_access:spare", group: "connections", state: "stopped", roles: [], profile_id: "spare" };
  const used = { id: "connection:main:connection_access:", group: "connections", state: "unknown", roles: ["collie"] };
  const service = { id: "provider-service:proxy:", group: "connections", state: "stopped" };
  expect(systemIssues([unused, unusedProfile, used, service])).toEqual([used, service]);
});

test("shared decisions preserve separate task failures and explicit ownership", () => {
  const decision = { id: "proposal-decision:4", project_id: "project", proposal_decision_id: 4,
    affected_tasks: [{ task_id: 7, owns_block: true }] };
  const task = { id: "task:7", project_id: "project", task_id: 7, blocked_kind: "proposal", block_decision_id: 4 };
  for (const separate of [
    { ...task, stalled: true },
    { ...task, allowance_condition: "verifier" },
    { ...task, blocked_kind: "operator" },
    { ...task, project_id: "other-project" },
    { ...task, block_decision_id: 5 },
    { ...task, block_decision_id: undefined },
  ]) expect(systemIssues([decision, separate])).toEqual([decision, separate]);
  for (const affected_tasks of [undefined, [{ task_id: 7, owns_block: false }]]) {
    const unowned = { ...decision, affected_tasks };
    expect(systemIssues([unowned, task])).toEqual([unowned, task]);
  }
});

test("a partial response preserves an independent task after its shared decision resolves", () => {
  const decision = { id: "proposal-decision:4", project_id: "project", proposal_decision_id: 4,
    condition: "Which checks apply?", affected_tasks: [{ task_id: 7, owns_block: true }] };
  const task = { id: "task:7", project_id: "project", task_id: 7, blocked_kind: "proposal", block_decision_id: 4 };
  const previous = acceptSystemStatus({}, { complete: true, conditions: [decision, task] }, 1);
  expect(systemIssues(previous.snapshot.conditions)).toEqual([decision]);
  const independent = { id: "task:7", project_id: "project", task_id: 7, blocked_kind: "proposal", condition: "Independent question" };
  const partial = acceptSystemStatus(previous, { complete: false, conditions: [independent] }, 2);
  expect(partial.unavailable).toBe(true);
  expect(systemIssues(partial.snapshot.conditions)).toEqual([decision, independent]);
  const current = acceptSystemStatus(partial, { complete: true, conditions: [independent] }, 3);
  expect(systemIssues(current.snapshot.conditions)).toEqual([independent]);
});

test("release status keeps a failed check separate from a current release", () => {
  expect(releaseView({ state: "unknown", installed: "a", available: "a" }).title).toBe("Release availability is unknown");
  expect(releaseView({ state: "current", installed: "a", available: "a" }).title).toBe("Culler is current");
  const pending = releaseView({ state: "current", available: "a", workflow: { state: "preparing", commit: "b", version: "0.2.0", url: "https://github.com/owner/repo/actions/runs/123" } });
  expect(pending.title).toBe("Culler is current");
  expect(pending.preparation).toBe("Version 0.2.0 is not available yet. Its workflow is in progress.");
  expect(releaseView({ state: "current", available: "a", workflow: { state: "failed", commit: "b", version: "0.2.0" } }).preparation)
    .toBe("The workflow for version 0.2.0 failed. The previous release remains available.");
  expect(releaseView({ state: "none", workflow: { state: "cancelled", commit: "b", version: "0.1.0" } }).preparation)
    .toBe("The workflow for version 0.1.0 stopped before it completed.");
  expect(pending.workflowURL).toEndWith("/123");
  expect(releaseView({ workflow: { url: "javascript:alert(1)" } }).workflowURL).toBeNull();
});

test("a completed workflow of a new version asks for another release check", () => {
  const view = releaseView({ state: "current", installed: "a", available: "a", workflow: { state: "published", commit: "b", version: "0.2.0" } });
  expect(view.available).toBe("a");
  expect(view.preparation).toBe("GitHub completed the workflow for version 0.2.0. Check for updates again to refresh the available release.");
  expect(releaseView({ available: "b", workflow: { state: "published", commit: "b", version: "0.2.0" } }).preparation).toBe("");
});

test("a main commit without a new version shows no workflow", () => {
  const view = releaseView({ state: "current", available: "a", workflow: { state: "unknown", url: "https://github.com/owner/repo/actions/runs/123" } });
  expect(view.preparation).toBe("");
  expect(view.workflowURL).toBeNull();
});

test("release labels show the version first and the short commit second", () => {
  const commit = "573c79b19fa334c59ab89679a01b7198443f94bc";
  const view = releaseView({ state: "available", installed: commit, available: "b".repeat(40), available_version: "0.1.0" });
  expect(view.installedLabel).toBe("573c79b19fa3");
  expect(view.availableLabel).toBe("0.1.0 (bbbbbbbbbbbb)");
  expect(releaseLabel("0.10.2", commit)).toBe("0.10.2 (573c79b19fa3)");
  expect(releaseLabel("v0.1.0", commit)).toBe("573c79b19fa3");
  expect(releaseView({}).installedLabel).toBe("unknown");
});

test("Current recovery removes only its own condition and preserves history references", () => {
  const x = { id: "connection:a:token_refresh:", diagnostic_id: 1 };
  const y = { id: "strategist:project", diagnostic_id: 2 };
  const failed = acceptSystemStatus({}, { complete: true, conditions: [x, y] }, 1);
  const recovered = acceptSystemStatus(failed, { complete: true, conditions: [y] }, 2);
  expect(recovered.snapshot.conditions).toEqual([y]);
  expect(failed.snapshot.conditions).toEqual([x, y]);
});

test("An incomplete response preserves the complete snapshot and marks it unavailable", () => {
  const previous = acceptSystemStatus({}, { complete: true, conditions: [{ id: "x" }] }, 1);
  const next = acceptSystemStatus(previous, { complete: false, conditions: [] }, 2);
  expect(next.snapshot).toBe(previous.snapshot);
  expect(next.unavailable).toBe(true);
});

test("A status refresh retains the last result until the request completes", async () => {
  let clock = 1000;
  let resolve;
  const updates: any[] = [];
  const timers = new Map<number, { fn: Function; delay: number }>();
  let serial = 0;
  const controller = createSystemStatus({
    request: () => new Promise(done => { resolve = done; }),
    changed: value => updates.push(value), now: () => clock,
    schedule: (fn, delay) => { const id = ++serial; timers.set(id, { fn, delay }); return id; },
    cancel: id => { timers.delete(id); },
  });
  resolve({ complete: true, conditions: [] });
  await controller.refresh();
  expect(updates.at(-1).unavailable).toBe(false);
  clock += 60_000;
  const tick = [...timers.values()].find(timer => timer.delay === 5000)!;
  tick.fn();
  expect(updates.at(-1).unavailable).toBe(false);
  resolve({ complete: true, conditions: [] });
  await controller.refresh();
  expect(updates.every(view => !view.unavailable)).toBe(true);
  for (const expected of [false, true]) {
    const failed = controller.refresh();
    resolve({ complete: false, conditions: [] });
    await failed;
    expect(updates.at(-1).unavailable).toBe(expected);
  }
  controller.stop();
});

test("Only the second consecutive failed status read shows the status as unavailable", async () => {
  let respond: (value: unknown) => void = () => {};
  let fail: (error: Error) => void = () => {};
  const updates: any[] = [];
  const controller = createSystemStatus({
    request: () => new Promise((done, reject) => { respond = done; fail = reject; }),
    changed: value => updates.push(value), now: () => 1000, schedule: () => 1, cancel: () => {},
  });
  const read = async (outcome: () => void) => { const pending = controller.refresh(); outcome(); await pending; return updates.at(-1); };
  expect(await read(() => respond({ complete: true, conditions: [{ id: "x" }] }))).toMatchObject({ unavailable: false, pending: false });
  const first = await read(() => respond({ complete: false, conditions: [{ id: "y" }] }));
  expect(first.unavailable).toBe(false);
  expect(first.snapshot.conditions.map((item: any) => item.id)).toEqual(["x", "y"]);
  expect((await read(() => fail(new Error("Status request expired.")))).unavailable).toBe(true);
  expect((await read(() => respond({ complete: true, conditions: [] }))).unavailable).toBe(false);
  expect((await read(() => fail(new Error("Status request expired.")))).unavailable).toBe(false);
  controller.stop();
});

test("A first load that fails twice leaves the loading state as unavailable", async () => {
  let fail: (error: Error) => void = () => {};
  const updates: any[] = [];
  const controller = createSystemStatus({
    request: () => new Promise((_, reject) => { fail = reject; }),
    changed: value => updates.push(value), schedule: () => 1, cancel: () => {},
  });
  for (const expected of [{ pending: true, unavailable: false }, { pending: false, unavailable: true }]) {
    const pending = controller.refresh();
    fail(new Error("System state is unavailable."));
    await pending;
    expect(updates.at(-1)).toMatchObject(expected);
  }
  controller.stop();
});

test("A failed status read in a hidden document waits for the next visible result", async () => {
  let hidden = false;
  let resolve;
  const updates: any[] = [];
  const controller = createSystemStatus({
    request: () => new Promise(done => { resolve = done; }),
    changed: value => updates.push(value), hidden: () => hidden,
    schedule: () => 1, cancel: () => {},
  });
  resolve({ complete: true, conditions: [] });
  await controller.refresh();
  const count = updates.length;
  const background = controller.refresh();
  hidden = true;
  resolve({ complete: false, conditions: [] });
  await background;
  expect(updates.length).toBe(count);
  hidden = false;
  const foreground = controller.refresh();
  expect(updates.at(-1).unavailable).toBe(false);
  resolve({ complete: true, conditions: [] });
  await foreground;
  expect(updates.at(-1).unavailable).toBe(false);
  controller.stop();
});

test("Concurrent refresh requests share one request and a stopped controller ignores its response", async () => {
  let resolve;
  let requests = 0;
  const updates: unknown[] = [];
  const timers: Function[] = [];
  const controller = createSystemStatus({
    request: () => { requests++; return new Promise(done => { resolve = done; }); },
    changed: value => updates.push(value), now: () => 1000,
    schedule: fn => { timers.push(fn); return timers.length; }, cancel: () => {},
  });
  void controller.refresh(); void controller.refresh();
  expect(requests).toBe(1);
  controller.stop();
  resolve({ complete: true, conditions: [] });
  await Promise.resolve(); await Promise.resolve();
  expect(updates.every(value => value.pending && !value.unavailable)).toBe(true);
});

test("A partial observation adds a known failure without clearing previous failures", () => {
  const previous=acceptSystemStatus({}, {complete:true,conditions:[{id:"x"}]},1);
  const next=acceptSystemStatus(previous,{complete:false,conditions:[{id:"y"}]},2);
  expect(next.snapshot.conditions.map(item=>item.id)).toEqual(["x","y"]);
  expect(next.unavailable).toBe(true);
});

test("A request that ignores cancellation releases the request slot after its deadline", async()=>{
  const timers: Function[]=[];
  let calls=0;
  const controller=createSystemStatus({request:()=>{calls++;return new Promise(()=>{});},changed:()=>{},now:()=>1,schedule:fn=>{timers.push(fn);return timers.length;},cancel:()=>{}});
  timers[0]();
  await Promise.resolve();await Promise.resolve();await Promise.resolve();
  void controller.refresh();
  expect(calls).toBe(2);
  controller.stop();
  timers[2]();
  await Promise.resolve();
});


test("history preserves each completed result and omits start records", () => {
  const events = [{id:1,outcome:"started"},{id:2,outcome:"failed"},{id:3,outcome:"started"},{id:4,outcome:"ready"},{id:5,outcome:"failed"}];
  expect(diagnosticResults(events).map(event => event.id)).toEqual([2,4,5]);
});

test("refresh activity distinguishes one request from several requests", () => {
  expect(refreshSummary({failures:1,requests:1})).toBe("1 failure from 1 refresh request");
  expect(refreshSummary({failures:2,requests:3})).toBe("2 failures from 3 refresh requests");
  expect(refreshSummary({})).toBe("0 failures from 0 refresh requests");
});


test("an unknown connection states that the cause is unconfirmed", () => {
  expect(conditionExplanation({group:"connections",state:"unknown"})).toBe("Culler cannot confirm the cause of this access issue.");
  expect(conditionExplanation({group:"connections",state:"unknown",provider_error_code:"invalid_grant"})).toBe("The provider rejected the credential refresh.");
});

test("a network failure states only the observed error and elapsed retry threshold", () => {
  const condition = {id:"connection:claude:token_refresh:",group:"connections",name:"Claude",state:"retry_wait",action:"",network_error:"connect",roles:["worker"],next_retry_at:"2026-09-26T10:00:30Z"};
  expect(conditionExplanation(condition)).toBe("Could not connect. The connection check did not complete.");
  expect(conditionRetry(condition)).toStartWith("Retry delay ended: ");
});

function deploymentHarness(options: { hidden?: boolean } = {}) {
  let clock = 1_000;
  let hidden = options.hidden ?? false;
  const timers: Array<{ fn: Function; delay: number; live: boolean }> = [];
  const pending: Array<{ resolve: Function; reject: Function }> = [];
  const updates: Array<{ doctor: unknown; pending: boolean }> = [];
  const check = createDeploymentCheck({
    request: () => new Promise((resolve, reject) => pending.push({ resolve, reject })),
    changed: (doctor, busy) => updates.push({ doctor, pending: busy }),
    hidden: () => hidden, now: () => clock,
    schedule: (fn, delay) => { timers.push({ fn, delay, live: true }); return timers.length - 1; },
    cancel: id => { if (id !== null && timers[id]) timers[id].live = false; },
  });
  const settle = async () => { for (let i = 0; i < 5; i++) await Promise.resolve(); };
  return {
    check, pending, updates, settle,
    live: () => timers.filter(timer => timer.live),
    advance: (ms: number) => { clock += ms; },
    hide: (value: boolean) => { hidden = value; },
    fire: () => { const timer = timers.filter(item => item.live).at(-1)!; timer.live = false; timer.fn(); },
  };
}

test("The deployment check runs when the page opens and again after each interval", async () => {
  const h = deploymentHarness();
  await h.settle();
  expect(h.pending.length).toBe(1);
  expect(h.updates.at(-1)).toEqual({ doctor: null, pending: true });
  expect(h.live().map(timer => timer.delay)).toEqual([DEPLOYMENT_CHECK_MS]);
  void h.check.refresh();
  await h.settle();
  expect(h.pending.length).toBe(1);
  h.pending[0].resolve({ ok: true, completed_at: "2026-09-23T10:00:00Z", findings: [] });
  await h.settle();
  expect(h.updates.at(-1)).toEqual({ doctor: { ok: true, completed_at: "2026-09-23T10:00:00Z", findings: [] }, pending: false });
  h.advance(DEPLOYMENT_CHECK_MS);
  h.fire();
  await h.settle();
  expect(h.pending.length).toBe(2);
  expect(h.live().length).toBe(1);
});

test("A hidden page skips the deployment check until it becomes visible again", async () => {
  const h = deploymentHarness({ hidden: true });
  await h.settle();
  expect(h.pending.length).toBe(0);
  expect(h.updates.length).toBe(0);
  h.advance(DEPLOYMENT_CHECK_MS);
  h.fire();
  await h.settle();
  expect(h.pending.length).toBe(0);
  h.hide(false);
  h.check.wake();
  h.check.wake();
  await h.settle();
  expect(h.pending.length).toBe(1);
});

test("A visible page repeats only a deployment check older than the interval", async () => {
  const h = deploymentHarness();
  await h.settle();
  h.pending[0].resolve({ ok: true, findings: [] });
  await h.settle();
  h.advance(DEPLOYMENT_CHECK_MS - 1);
  h.check.wake();
  await h.settle();
  expect(h.pending.length).toBe(1);
  h.advance(1);
  h.check.wake();
  await h.settle();
  expect(h.pending.length).toBe(2);
});

test("A failed or incomplete deployment check retains the earlier evidence", async () => {
  const h = deploymentHarness();
  const earlier = { ok: true, completed_at: "2026-09-23T10:00:00Z", findings: [{ check: "spine", status: "ok" }] };
  await h.settle();
  h.pending[0].resolve(earlier);
  await h.settle();
  const manual = h.check.refresh();
  await h.settle();
  h.pending[1].reject(new Error("The request exceeded its deadline. Retry the read."));
  await expect(manual).rejects.toThrow("deadline");
  expect(h.updates.at(-1)).toEqual({ doctor: { ...earlier, unavailable: true }, pending: false });
  void h.check.refresh();
  await h.settle();
  h.pending[2].resolve({ deployment_complete: false, completed_at: "2026-09-23T10:02:00Z", findings: [] });
  await h.settle();
  expect(h.updates.at(-1)!.doctor).toEqual({ ...earlier, unavailable: true, attempted_at: "2026-09-23T10:02:00Z" });
});

test("Connections that require attention stay open and the others share one count", () => {
  const connections = [
    { id: "connection:a:connection_access:", state: "ready", roles: ["worker"] },
    { id: "connection:b:connection_access:", state: "stopped", roles: ["collie"] },
    { id: "connection:c:connection_access:", state: "unknown", roles: [] },
    { id: "connection:d:connection_access:", state: "stopped", roles: [], diagnostic_id: 4 },
    { id: "connection:e:connection_access:", state: "unknown", roles: ["scribe"] },
  ];
  const groups = connectionGroups(connections, [connections[1], connections[3]]);
  expect(groups.attention.map(item => item.id)).toEqual([connections[1].id, connections[4].id]);
  expect(groups.connected.map(item => item.id)).toEqual([connections[0].id]);
  expect(groups.unused.map(item => item.id)).toEqual([connections[2].id, connections[3].id]);
  expect(connectionDisclosureLabel(groups)).toBe("1 connected, 2 unused");
  expect(connectionDisclosureLabel({ connected: [], unused: [{}, {}] })).toBe("2 unused");
});

test("The viewer facts follow the latest response that carries them", () => {
  const viewers = { agentsview: { url: "http://127.0.0.1:7334", state: "available", reason: "" }, wiki: { url: "http://127.0.0.1:7335", projects: {} } };
  const complete = acceptSystemStatus({}, { complete: true, conditions: [], viewers }, 1);
  expect(complete.viewers).toEqual(viewers);
  const stopped = { ...viewers, agentsview: { ...viewers.agentsview, state: "unavailable", reason: "Stopped." } };
  const partial = acceptSystemStatus(complete, { complete: false, conditions: [], viewers: stopped }, 2);
  expect(partial.viewers).toEqual(stopped);
  expect(acceptSystemStatus(partial, { complete: false, conditions: [] }, 3).viewers).toEqual(stopped);
  expect(acceptSystemStatus(partial, { complete: true, conditions: [] }, 4).viewers).toBeNull();
});

test("A condition without a dashboard control shows its repair action as text", () => {
  const agentsview = { id: "agentsview", group: "execution", state: "failed", name: "AgentsView", condition: "Trace links do not open.", action: "Run ./redeploy.sh." };
  expect(actionControl(agentsview)).toBe(false);
  expect(repairText(agentsview)).toBe("Run ./redeploy.sh.");
  expect(conditionTitle(agentsview)).toBe("AgentsView");
  expect(conditionImpact(agentsview)).toBe("Trace links do not open.");
  const strategist = { id: "infrastructure:alpha", group: "execution", state: "stopped", name: "Strategist", action: "Resume Project infrastructure" };
  expect(actionControl(strategist)).toBe(true);
  expect(repairText(strategist)).toBe("");
  expect(conditionTitle(strategist)).toBe("Strategist · infra failed");
  const connection = { id: "c", group: "connections", connection_id: "c", state: "failed", name: "Claude", action: "Reconnect" };
  expect(actionControl(connection)).toBe(true);
  expect(repairText({ ...agentsview, action: "View details" })).toBe("");
  expect(actionControl({ ...agentsview, action: "" })).toBe(false);
});


test("network notices distinguish delay, eligibility, and an active attempt", () => {
  const base = { group: "connections", state: "retry_wait", profile_id: "review", retry_phase: "delay" };
  expect(conditionSummary(base)).toBe("Connection interrupted");
  expect(conditionSummary({ ...base, retry_phase: "pending" })).toBe("Retry pending");
  const active = { ...base, attempts: [{ role: "verifier", run_id: "a", task_id: 2, state: "active" }] };
  expect(conditionSummary(active)).toBe("Verifier retry in progress");
  expect(conditionImpact(active)).toContain("task #2");
  expect(conditionImpact(active)).not.toContain("cannot start");
  expect(conditionImpact({ ...active, attempts: [{ role: "verifier", state: "stale" }] })).toContain("heartbeat is stale");
});

test("confirmed recovery appears once and follows the attempt's current state", () => {
  const failure = { id: "x", diagnostic_id: 1, state: "retry_wait" };
  const success = { id: "x", diagnostic_id: 2, resolved_diagnostic_id: 1, state: "ready", recovery: { role: "verifier", state: "active", task_id: 2 } };
  const failed = acceptSystemStatus({}, { complete: true, conditions: [failure] }, 0);
  const result = { complete: true, conditions: [], connections: [success] };
  const first = acceptSystemStatus(failed, result, 1);
  expect(first.recovered).toEqual([success]);
  expect(recoveryText(success)).toBe("The model responded. The Verifier continues on task #2.");
  expect(acceptSystemStatus(first, result, 5000).recoveredUntil).toBe(first.recoveredUntil);
  expect(acceptSystemStatus(first, result, 11000).recovered).toEqual([]);
  expect(acceptSystemStatus(failed, { ...result, connections: [{ ...success, resolved_diagnostic_id: 9 }] }, 1).recovered).toEqual([]);
  expect(recoveryText({ ...success, recovery: { role: "verifier", state: "completed" } })).not.toContain("continues");
});

test("priority task notices identify the Project hold and current cause", () => {
  for (const allowance_condition of ["verifier", ""]) {
    const condition = { task_id: 6, project_id: "alpha", project_priority: true, allowance_condition,
      condition: "The required behavior needs an operator decision." };
    expect(conditionImpact(condition)).toContain("Task #6 prevents new work in Project alpha.");
    expect(conditionImpact(condition)).toContain("You must address this task before unrelated Project work can continue.");
    expect(conditionExplanation(condition)).toBe(condition.condition);
  }
  expect(conditionImpact({ task_id: 6, project_priority: false })).not.toContain("prevents new work");
});

test("host and allowance failures show repair instructions without a sign-in control", () => {
  for (const action of ["Restore credential access", "Restore provider access", "Review request limits", "Review account allowance"]) {
    const condition = {action, connection_id:"saved-connection"};
    expect(actionControl(condition)).toBe(false);
    expect(repairText(condition)).not.toBe(action);
  }
  expect(actionControl({action:"Replace API key", connection_id:"saved-connection"})).toBe(true);
});


test("a profile retry offers a model test before and after its retry time", () => {
  const condition = { group: "connections", connection_id: "team", profile_id: "review", state: "retry_wait", action: "" };
  for (const retry_phase of ["delay", "pending", "active"]) {
    expect(actionControl({ ...condition, retry_phase })).toBe(true);
  }
  expect(actionControl({ ...condition, profile_id: "" })).toBe(false);
  expect(actionControl({ ...condition, state: "ready" })).toBe(false);
});


test("connection titles use the profile name instead of its identifier", () => {
  const condition = { group: "connections", name: "Subscription account", profile_id: "profile-internal", profile_name: "big-opus", state: "retry_wait", retry_phase: "pending" };
  expect(conditionTitle(condition)).toBe("Subscription account / big-opus · Retry pending");
  expect(conditionTitle({ ...condition, profile_name: undefined, model: "review-model" })).toBe("Subscription account / review-model · Retry pending");
  expect(conditionTitle({ ...condition, profile_name: undefined })).toBe("Subscription account / Agent profile · Retry pending");
});

test("a conversation rejection presents the reply action", () => {
  for (const provider_error_code of ["invalid_encrypted_content", "invalid_request_error"]) {
    const reply = { id: "reply:1", name: "Collie", group: "execution", state: "failed", action: "Retry reply", session_id: "h-1", attempt_id: "a-1", provider_error_code };
    expect(systemIssues([reply])).toEqual([reply]);
    expect(actionControl(reply)).toBe(true);
    expect(conditionTitle(reply)).toContain("Collie");
    expect(conditionTitle(reply)).not.toContain("Model access");
    expect(repairText(reply)).not.toContain("connection");
  }
});
