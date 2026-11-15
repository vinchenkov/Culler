import type { Database } from "bun:sqlite";
import { createHash } from "node:crypto";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

/** One file entry of a change data fixture, with its part of the patch. */
export type ChangeFixtureFile = {
  status: "added" | "copied" | "deleted" | "modified" | "renamed" | "type-changed";
  path: string;
  previous_path?: string;
  old_mode?: string;
  new_mode?: string;
  similarity?: number;
  binary?: boolean;
  patch: string;
};

/** The change data of a packet: the host directory of the artifact plane and
 * the changed files of the merge candidate. */
export type ChangeFixture = {
  plane: string;
  files: ChangeFixtureFile[];
};

type PacketOptions = {
  taskID: number;
  thesis: string;
  commit?: string;
  /** The input commit and the destination commit. */
  input?: string;
  renderPath?: string;
  reportSHA256?: string;
  createdAt?: string;
  changes?: ChangeFixture;
};

const sha256 = (body: string | Uint8Array) => createHash("sha256").update(body).digest("hex");

// Writes the file inventory and the patch of a merge candidate as the
// repository component records them, and returns the change data record.
function writeChangeData(fixture: ChangeFixture, project: string, taskID: number, run: string, from: string, to: string) {
  const { files } = fixture;
  const name = "candidate";
  const relative = join(String(taskID), "changes", run);
  const container = join(process.env["MC_ARTIFACTS_ROOT"]!, project, relative);
  mkdirSync(join(fixture.plane, relative), { recursive: true });
  const encoder = new TextEncoder();
  let offset = 0;
  const entries = files.map(({ patch, ...file }) => {
    const bytes = encoder.encode(patch).length;
    const hunks = patch.split("\n");
    const body = hunks.slice(hunks.findIndex((line) => line.startsWith("@@")));
    const text = hunks.some((line) => line.startsWith("@@"));
    const entry = {
      ...file,
      additions: text ? body.filter((line) => line.startsWith("+")).length : 0,
      deletions: text ? body.filter((line) => line.startsWith("-")).length : 0,
      binary: file.binary ?? false, text_diff: text, patch_offset: offset, patch_bytes: bytes,
    };
    offset += bytes;
    return entry;
  });
  const patch = files.map((file) => file.patch).join("");
  const inventory = JSON.stringify({ from_commit: from, to_commit: to, patch_bytes: offset, patch_sha256: sha256(patch), files: entries });
  writeFileSync(join(fixture.plane, relative, `${name}.patch`), patch);
  writeFileSync(join(fixture.plane, relative, `${name}.json`), inventory);
  return {
    from_commit: from, to_commit: to,
    inventory_path: join(container, `${name}.json`), inventory_sha256: sha256(inventory),
    patch_path: join(container, `${name}.patch`), patch_sha256: sha256(patch), patch_bytes: offset, files: files.length,
  };
}

// Browser fixtures represent completed repository work through the current schema.
// A repository result requires its active execution, so each result runs inside one.
function repositoryExecution(db: Database) {
  return (project: string, taskID: number, runID: string, operation: string, target: string, record: () => void) => {
    db.run("INSERT INTO execution_owners DEFAULT VALUES");
    db.run(`INSERT INTO runs(id,tier,role,project,subject,session_path,binding)
      VALUES (?,'pipeline','worker',?,?,?,'mechanical')`, [runID, project, taskID, `sessions/${runID}`]);
    db.run(`INSERT INTO repository_executions(run_id,project,task_id,operation,target,owner,effect,boot_id,
      heartbeat_uptime_ms,deadline_uptime_ms) VALUES (?,?,?,?,?,(SELECT MAX(owner) FROM execution_owners),'{}','fixture',0,?)`,
    [runID, project, taskID, operation, target, operation === "apply" ? 600000 : 300000]);
    record();
    db.run("UPDATE runs SET ended_at=datetime('now'),outcome='completed' WHERE id=?", [runID]);
    db.run(`UPDATE repository_executions SET ended_at=datetime('now'),end_reason='result',stopped_at=datetime('now')
      WHERE run_id=?`, [runID]);
  };
}

type SubmissionOptions = Pick<PacketOptions, "taskID" | "commit" | "input" | "renderPath">;

// The Worker submission of a proposed or seeded task. The task is Worked after it.
function recordWorkerSubmission(db: Database, options: SubmissionOptions) {
  const task = db.query(`SELECT t.project,t.status,t.requirements_version,COALESCE(t.description,'') AS description,
    t.destination_branch,t.correction_open,(SELECT COUNT(*) FROM task_versions v WHERE v.task_id=t.id) AS versions
    FROM tasks t WHERE t.id=?`).get(options.taskID) as {
      project: string; status: string; requirements_version: number; description: string; destination_branch: string;
      correction_open: number; versions: number;
    } | null;
  if (!task || !["proposed", "seeded"].includes(task.status)) {
    throw new Error("The delivery fixture requires a proposed or seeded repository task.");
  }
  const execution = repositoryExecution(db);
  // A packet after a correction uses its own runs and commit.
  const key = task.versions === 0 ? String(options.taskID) : `${options.taskID}-${task.versions}`;
  const commit = options.commit ?? createHash("sha1").update(`smoke-task-${key}`).digest("hex");
  const input = options.input ?? createHash("sha1").update(`smoke-input-${options.taskID}`).digest("hex");
  const renderPath = options.renderPath ?? `/mc/artifacts/${task.project}/${options.taskID}/packet/report.html`;
  const run = (role: string, suffix = role) => {
    const id = `packet-fixture-${key}-${suffix}`;
    db.run(`INSERT INTO runs(id,tier,role,project,subject,ended_at,outcome,binding)
      VALUES (?,'pipeline',?,?,?,datetime('now'),'completed','fake/fake')`,
    [id, role, task.project, options.taskID]);
    return id;
  };
  const repositoryRun = (id: string, assignedInput: string, versionID: number | null) => {
    db.run(`INSERT INTO repository_runs(run_id,task_id,input_commit,destination_commit,version_id,temporary_bytes,captured,cleaned)
      VALUES (?,?,?,?,?,0,1,1)`, [id, options.taskID, assignedInput, input, versionID]);
  };
  const submit = (id: string, assignedInput: string) => {
    db.run("INSERT INTO version_submissions(run_id,task_id,input_commit,result_commit,evidence_path) VALUES (?,?,?,?,?)",
      [id, options.taskID, assignedInput, commit, renderPath]);
    const saved = db.run(`INSERT INTO task_versions(task_id,run_id,input_commit,result_commit,destination_branch,retained_ref,evidence_path)
      VALUES (?,?,?,?,?,?,?)`, [options.taskID, id, assignedInput, commit, task.destination_branch,
      `refs/mc/tasks/${options.taskID}/versions/${id}`, renderPath]);
    const versionID = Number(saved.lastInsertRowid);
    execution(task.project, options.taskID, `${id}-capture`, "capture", id,
      () => db.run("UPDATE version_submissions SET captured_version=? WHERE run_id=?", [versionID, id]));
    return versionID;
  };

  if (task.status === "proposed") db.run("UPDATE tasks SET status='seeded' WHERE id=?", [options.taskID]);
  const worker = run("worker");
  repositoryRun(worker, input, null);
  const versionID = submit(worker, input);
  db.run("UPDATE tasks SET submitted_version=?,input_commit=?,base_commit=COALESCE(base_commit,?) WHERE id=?",
    [versionID, input, input, options.taskID]);
  db.run("UPDATE tasks SET status='worked',completion_sha=? WHERE id=?", [commit, options.taskID]);
  return { task, commit, input, renderPath, run, repositoryRun, versionID };
}

/** A Worked task: one Worker submission waits for the Verifier. The input
 * commit is the base commit of the task. */
export function seedWorkerSubmission(db: Database, options: SubmissionOptions): { versionID: number; commit: string; input: string } {
  return db.transaction(() => {
    const { versionID, commit, input } = recordWorkerSubmission(db, options);
    return { versionID, commit, input };
  })();
}

export function seedDeliveryPacket(db: Database, options: PacketOptions): { packetID: number; versionID: number } {
  return db.transaction(() => {
    const { task, commit, input, renderPath, run, repositoryRun, versionID } = recordWorkerSubmission(db, options);
    const reportHash = options.reportSHA256 ?? createHash("sha256").update(options.thesis).digest("hex");
    const createdAt = options.createdAt ?? new Date().toISOString().replace("T", " ").slice(0, 19);

    const verifier = run("verifier");
    repositoryRun(verifier, commit, versionID);
    db.run("INSERT INTO version_verdicts(version_id,run_id,outcome,evidence_path) VALUES (?,?,'pass',?)", [versionID, verifier, renderPath]);
    db.run("UPDATE runs SET verdict_outcome='pass',evidence_path=? WHERE id=?", [renderPath, verifier]);
    db.run("UPDATE tasks SET source_version=? WHERE id=?", [versionID, options.taskID]);
    db.run("UPDATE tasks SET status='verified' WHERE id=?", [options.taskID]);
    if (task.correction_open === 1) {
      db.run("INSERT INTO correction_results(version_id,task_id,outcome) VALUES (?,?,'pass')", [versionID, options.taskID]);
      db.run("UPDATE tasks SET correction_count=0,verifier_correction_open=0,correction_open=0 WHERE id=?", [options.taskID]);
    }

    // The packet holds the verified Worker version.
    const packager = run("packager");
    repositoryRun(packager, commit, versionID);
    db.run("UPDATE runs SET output_path=? WHERE id=?", [renderPath, packager]);
    db.run("UPDATE tasks SET status='packaged' WHERE id=?", [options.taskID]);
    db.run("UPDATE tasks SET report_path=?,report_sha256=?,thesis=? WHERE id=?",
      [renderPath, reportHash, options.thesis, options.taskID]);
    const preparation = run("worker", "merge");
    const fixture = options.changes;
    const changes = fixture && { candidate: writeChangeData(fixture, task.project, options.taskID, preparation, input, commit) };
    const candidate = {
      submitted_commit: commit, destination_branch: task.destination_branch, destination_commit: input,
      commit, status: "merged",
      ...(changes ? { changes } : {}),
    };
    const prepared = db.run(`INSERT INTO merge_candidates(task_id,run_id,version_id,destination_branch,destination_commit,
      candidate_commit,result,status,retained_ref) VALUES (?,?,?,?,?,?,?,'merged',?)`,
    [options.taskID, preparation, versionID, task.destination_branch, input, commit,
      JSON.stringify(candidate), `refs/mc/tasks/${options.taskID}/candidates/${preparation}`]);
    const candidateID = Number(prepared.lastInsertRowid);
    db.run("UPDATE tasks SET candidate_id=? WHERE id=?", [candidateID, options.taskID]);
    const packet = db.run(`INSERT INTO review_versions(task_id,version_id,candidate_id,render_path,report_sha256,thesis,criteria,requirements_version,created_at)
      VALUES (?,?,?,?,?,?,?,?,?)`, [options.taskID, versionID, candidateID, renderPath, reportHash, options.thesis, task.description, task.requirements_version, createdAt]);
    db.run("UPDATE tasks SET current_review_id=?,review_slot_held=1 WHERE id=?",
      [Number(packet.lastInsertRowid), options.taskID]);
    return { packetID: Number(packet.lastInsertRowid), versionID };
  })();
}

// The merge of an approved packet: the apply execution records the receipt of
// the merge candidate and archives the task.
export function completeMerge(db: Database, taskID: number): string {
  return db.transaction(() => {
    const task = db.query(`SELECT t.project,t.submitted_version AS version_id,t.candidate_id,t.current_review_id AS packet_id,
      c.destination_branch,c.candidate_commit FROM tasks t JOIN merge_candidates c ON c.id=t.candidate_id
      WHERE t.id=? AND t.decision='approved' AND t.archived=0`).get(taskID) as {
        project: string; version_id: number; candidate_id: number; packet_id: number;
        destination_branch: string; candidate_commit: string;
      } | null;
    if (!task) throw new Error("The merge fixture requires an approved task with a merge candidate.");
    const runID = `merge-fixture-${taskID}-apply`;
    repositoryExecution(db)(task.project, taskID, runID, "apply", `task/${taskID}`, () => {
      db.run(`INSERT INTO merge_operations(run_id,task_id,project,operation,version_id,candidate_id,destination_branch,packet_id)
        VALUES (?,?,?,'apply',?,?,?,?)`, [runID, taskID, task.project, task.version_id, task.candidate_id,
        task.destination_branch, task.packet_id]);
      db.run("UPDATE merge_operations SET result=?,finished=1 WHERE run_id=?",
        [JSON.stringify({ applied: { authorization: runID, commit: task.candidate_commit, recovered: false } }), runID]);
      db.run("UPDATE tasks SET archived=1 WHERE id=?", [taskID]);
    });
    return task.candidate_commit;
  })();
}
