// changes.js — pure view models for the Files changed view of the Review page.
// The change data of a packet holds one comparison: the Git range from the
// destination commit through the merge candidate commit. The comparison has a
// file inventory and a patch that the repository component derives from Git.

import { shortCommit } from "./review.js";

/** The change data record of a packet row, or null. */
export function changeRecord(row) {
  const record = row?.candidate_changes;
  return record && typeof record === "object" && typeof record.from_commit === "string" ? record : null;
}

/** The label of the Files changed control: the number of changed files when
 * the packet records it. */
export function changesLabel(row) {
  const count = changeRecord(row)?.files;
  return typeof count === "number" ? `Files changed · ${count}` : "Files changed";
}

/** The commits of the comparison, in display order: the two commits of the
 * range, then the verified Worker commit. A copy is the full commit
 * identifier. */
export function comparisonFacts(row) {
  const record = changeRecord(row);
  const p = row ?? {};
  const facts = [];
  const commit = (key, label, value, detail = "") => {
    if (typeof value === "string" && value !== "") facts.push({ key, label, detail, text: shortCommit(value), title: value, copy: value });
  };
  commit("destination", "Destination commit", record?.from_commit ?? p.destination_commit,
    typeof p.destination_branch === "string" ? p.destination_branch : "");
  commit("candidate", "Merge candidate commit", record?.to_commit ?? p.candidate_commit);
  commit("worker", "Verified Worker commit", p.submitted_commit);
  return facts;
}

const STATUS = {
  added: { label: "Added", tree: "added", tone: "leaf" },
  copied: { label: "Copied", tree: "added", tone: "leaf" },
  deleted: { label: "Deleted", tree: "deleted", tone: "rust" },
  modified: { label: "Modified", tree: "modified", tone: "sky" },
  renamed: { label: "Renamed", tree: "renamed", tone: "stone" },
  "type-changed": { label: "Type changed", tree: "modified", tone: "stone" },
};

/** The label and tone of a change type, and the Git status that the file
 * tree shows. */
export function fileStatus(file) {
  return STATUS[file?.status] ?? { label: "Changed", tree: "modified", tone: "stone" };
}

const MODES = {
  "100644": "file",
  "100755": "executable file",
  "120000": "symbolic link",
  "160000": "submodule",
  "040000": "directory",
};

/** A Git file mode with its meaning, for example "100755 (executable file)". */
export function modeLabel(mode) {
  return MODES[mode] ? `${mode} (${MODES[mode]})` : String(mode ?? "");
}

/** The notes of one file entry. Each change without a text diff states its
 * cause, so that the view keeps an unavailable text diff apart from an
 * unchanged file. */
export function fileNotes(file) {
  const notes = [];
  const moved = file.status === "renamed" || file.status === "copied";
  if (moved) {
    const verb = file.status === "renamed" ? "Renamed" : "Copied";
    notes.push(`${verb} from ${file.previous_path} with ${file.similarity ?? 0}% similarity.`);
  }
  if (file.status === "type-changed") {
    notes.push(`The file type changed from ${modeLabel(file.old_mode)} to ${modeLabel(file.new_mode)}.`);
  } else if (file.old_mode && file.new_mode && file.old_mode !== file.new_mode) {
    notes.push(`The file mode changed from ${modeLabel(file.old_mode)} to ${modeLabel(file.new_mode)}.`);
  }
  if (file.binary) {
    notes.push("Git treats this file as binary. The comparison holds no text diff for it.");
  } else if (!file.text_diff) {
    if (file.status === "added") notes.push("The new file is empty.");
    else if (file.status === "deleted") notes.push("The deleted file was empty.");
    else if (moved || (file.old_mode && file.new_mode && file.old_mode !== file.new_mode)) notes.push("The file content is unchanged.");
    else notes.push("The comparison holds no text diff for this change.");
  }
  return notes;
}

/** The line counts of one file, for example "+12 −3", or "binary". */
export function lineCounts(file) {
  return file.binary ? "binary" : `+${file.additions ?? 0} −${file.deletions ?? 0}`;
}

/** The view shows a diff above these sizes only after the operator asks. */
export const DEFERRED_LINES = 2000;
export const DEFERRED_BYTES = 256 * 1024;

/** Whether the view waits for the operator before it shows the diff. */
export function deferredDiff(file) {
  return Boolean(file.text_diff) &&
    ((file.additions ?? 0) + (file.deletions ?? 0) > DEFERRED_LINES || (file.patch_bytes ?? 0) > DEFERRED_BYTES);
}

/** A size in bytes as text, for example "312 KiB". */
export function sizeLabel(bytes) {
  if (bytes < 1024) return `${bytes} bytes`;
  if (bytes < 1024 * 1024) return `${Math.round(bytes / 1024)} KiB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
}

/** The Git status entries of the file tree, one for each file. */
export function treeStatus(files) {
  return (files ?? []).map((file) => ({ path: file.path, status: fileStatus(file).tree }));
}

const compareNames = (a, b) => (a < b ? -1 : a > b ? 1 : 0);

/** The order of two file paths in the file tree. At the first segment that
 * differs, a directory comes before a file. Names use code unit order. */
export function comparePaths(a, b) {
  const left = a.split("/"), right = b.split("/");
  for (let i = 0; i < Math.min(left.length, right.length); i++) {
    if (left[i] === right[i]) continue;
    const leftDirectory = i < left.length - 1, rightDirectory = i < right.length - 1;
    if (leftDirectory !== rightDirectory) return leftDirectory ? -1 : 1;
    return compareNames(left[i], right[i]);
  }
  return left.length - right.length;
}

/** The file paths in the order of the file tree. */
export function fileOrder(files) {
  return (files ?? []).map((file) => file.path).sort(comparePaths);
}

/** The index of each file path in the file inventory. */
export function fileIndex(files) {
  return new Map((files ?? []).map((file, index) => [file.path, index]));
}

/** Whether the view can show the unmodified lines of a file: a text change
 * with a version of the file at each commit of the comparison. */
export function expandableFile(file) {
  return Boolean(file?.text_diff) && !file.binary && ["modified", "renamed", "copied"].includes(file.status);
}

/** The request address of the change data of a packet, or of one file of
 * that change data: its patch part or its version at each commit. */
export function changesAddress(row, file = null, part = "patch") {
  const query = new URLSearchParams({ packet: String(row.packet_id), version: String(row.version_id) });
  if (file !== null) query.set("file", String(file));
  return `/api/packets/${row.task_id}/changes${file === null ? "" : `/${part}`}?${query}`;
}

/** The state of the comparison before the view reads its change data:
 * "expired", "absent", or "ready". */
export function comparisonState(row, view) {
  if (view?.expired) return "expired";
  return changeRecord(row) ? "ready" : "absent";
}

/** The text of a comparison that the view cannot show. */
export function comparisonStateText(state, view, row = null) {
  if (state === "expired") return `The change data expired on ${view?.expiredAt ?? "an earlier date"}. The repository keeps the commits.`;
  return typeof row?.candidate_commit === "string" && row.candidate_commit !== ""
    ? "Culler recorded no change data for this merge candidate."
    : "No merge candidate is available for this packet.";
}

