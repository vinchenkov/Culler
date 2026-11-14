// Unit tests for changes.js — the view models of the Files changed view.

import { describe, expect, test } from "bun:test";
import {
  DEFERRED_BYTES,
  DEFERRED_LINES,
  changeRecord,
  changesAddress,
  changesLabel,
  comparePaths,
  comparisonFacts,
  comparisonState,
  comparisonStateText,
  deferredDiff,
  expandableFile,
  fileNotes,
  fileOrder,
  fileStatus,
  lineCounts,
  modeLabel,
  sizeLabel,
  treeStatus,
} from "./changes.js";

const sha = (c: string) => c.repeat(40);
const record = (from: string, to: string, files = 3) => ({
  from_commit: sha(from), to_commit: sha(to), files,
  inventory_path: "/mc/artifacts/ws/7/changes/run/candidate.json", inventory_sha256: sha("0").slice(0, 64),
  patch_path: "/mc/artifacts/ws/7/changes/run/candidate.patch", patch_sha256: sha("1").slice(0, 64), patch_bytes: 100,
});
const row = {
  task_id: 7, packet_id: 3, version_id: 9,
  input_commit: sha("b"), submitted_commit: sha("c"),
  destination_branch: "culler", destination_commit: sha("d"), candidate_commit: sha("e"),
  candidate_changes: record("d", "e", 12),
  verifier_evidence: "/mc/artifacts/ws/7/evidence/verify.md",
};
const file = (over: Record<string, unknown> = {}) => ({
  status: "modified", path: "src/a.go", old_mode: "100644", new_mode: "100644",
  additions: 3, deletions: 1, binary: false, text_diff: true, patch_offset: 0, patch_bytes: 200, ...over,
});

describe("changeRecord", () => {
  test("returns the record of the packet, or null without one", () => {
    expect(changeRecord(row)?.from_commit).toBe(sha("d"));
    expect(changeRecord(row)?.to_commit).toBe(sha("e"));
    expect(changeRecord({ ...row, candidate_changes: null })).toBeNull();
    expect(changeRecord({ ...row, candidate_changes: {} })).toBeNull();
    expect(changeRecord(null)).toBeNull();
  });
});

describe("changesLabel", () => {
  test("states the number of changed files of the merge candidate", () => {
    expect(changesLabel(row)).toBe("Files changed · 12");
    expect(changesLabel({ ...row, candidate_changes: null })).toBe("Files changed");
  });
});

describe("comparisonFacts", () => {
  test("names the destination branch, both commits of the range, and the verified Worker commit", () => {
    const facts = comparisonFacts(row);
    expect(facts.map((f) => [f.key, f.label, f.detail, f.copy])).toEqual([
      ["destination", "Destination commit", "culler", sha("d")],
      ["candidate", "Merge candidate commit", "", sha("e")],
      ["worker", "Verified Worker commit", "", sha("c")],
    ]);
    expect(facts[0]).toEqual({ key: "destination", label: "Destination commit", detail: "culler", text: "ddddddd", title: sha("d"), copy: sha("d") });
  });

  test("the commits of the range come from the record", () => {
    const facts = comparisonFacts({ ...row, destination_commit: sha("1"), candidate_commit: null, candidate_changes: record("8", "9") });
    expect(facts.slice(0, 2).map((f) => f.copy)).toEqual([sha("8"), sha("9")]);
  });


  test("shows only the recorded commits", () => {
    expect(comparisonFacts({ input_commit: sha("b"), submitted_commit: sha("c") }).map((f) => [f.key, f.copy])).toEqual([["worker", sha("c")]]);
    expect(comparisonFacts(null)).toEqual([]);
  });
});

describe("fileStatus and fileNotes", () => {
  test("each change type has a label and a tree status", () => {
    expect(["added", "copied", "deleted", "modified", "renamed", "type-changed"].map((status) => fileStatus({ status }).tree))
      .toEqual(["added", "added", "deleted", "modified", "renamed", "modified"]);
    expect(fileStatus({ status: "type-changed" }).label).toBe("Type changed");
  });

  test("a rename states the previous path and the similarity", () => {
    expect(fileNotes(file({ status: "renamed", previous_path: "old/a.go", similarity: 100, text_diff: false, additions: 0, deletions: 0 })))
      .toEqual(["Renamed from old/a.go with 100% similarity.", "The file content is unchanged."]);
  });

  test("a mode change states both modes, and an unchanged content is explicit", () => {
    expect(fileNotes(file({ new_mode: "100755", text_diff: false, additions: 0, deletions: 0 }))).toEqual([
      "The file mode changed from 100644 (file) to 100755 (executable file).",
      "The file content is unchanged.",
    ]);
  });

  test("a binary change has no text diff and says so", () => {
    expect(fileNotes(file({ binary: true, text_diff: false }))).toEqual([
      "Git treats this file as binary. The comparison holds no text diff for it.",
    ]);
  });

  test("an empty new or deleted file is explicit", () => {
    expect(fileNotes(file({ status: "added", old_mode: undefined, text_diff: false, additions: 0 }))).toEqual(["The new file is empty."]);
    expect(fileNotes(file({ status: "deleted", new_mode: undefined, text_diff: false, deletions: 0 }))).toEqual(["The deleted file was empty."]);
  });

  test("a type change states both types, and a text diff needs no note", () => {
    expect(fileNotes(file({ status: "type-changed", new_mode: "120000" }))).toEqual([
      "The file type changed from 100644 (file) to 120000 (symbolic link).",
    ]);
    expect(fileNotes(file())).toEqual([]);
  });

  test("an unknown mode shows as recorded", () => {
    expect(modeLabel("100600")).toBe("100600");
  });
});

describe("lineCounts and deferredDiff", () => {
  test("shows the added and removed lines, or binary", () => {
    expect(lineCounts(file())).toBe("+3 −1");
    expect(lineCounts(file({ binary: true }))).toBe("binary");
  });

  test("a large text diff waits for the operator", () => {
    expect(deferredDiff(file())).toBe(false);
    expect(deferredDiff(file({ additions: DEFERRED_LINES + 1, deletions: 0 }))).toBe(true);
    expect(deferredDiff(file({ patch_bytes: DEFERRED_BYTES + 1 }))).toBe(true);
    expect(deferredDiff(file({ patch_bytes: DEFERRED_BYTES + 1, text_diff: false }))).toBe(false);
  });

  test("sizes read in bytes, kibibytes, or mebibytes", () => {
    expect(sizeLabel(900)).toBe("900 bytes");
    expect(sizeLabel(324 * 1024)).toBe("324 KiB");
    expect(sizeLabel(3 * 1024 * 1024)).toBe("3.0 MiB");
  });
});

describe("fileOrder and comparePaths", () => {
  test("the file order is the order of the tree: directories first, then names", () => {
    const files = ["z.txt", "b/c.txt", "a.txt", "b/a/d.txt", "B.txt", "a/x.txt"].map((path) => ({ path }));
    expect(fileOrder(files)).toEqual(["a/x.txt", "b/a/d.txt", "b/c.txt", "B.txt", "a.txt", "z.txt"]);
  });

  test("a file and a directory of one name keep a stable order", () => {
    expect(comparePaths("docs", "docs/a.md")).toBeLessThan(0);
    expect(comparePaths("docs/a.md", "docs/a.md")).toBe(0);
  });

  test("the tree status holds one entry for each file", () => {
    expect(treeStatus([file({ status: "added" }), file({ path: "x", status: "type-changed" })])).toEqual([
      { path: "src/a.go", status: "added" },
      { path: "x", status: "modified" },
    ]);
  });
});

describe("changesAddress", () => {
  test("names the exact packet version and the file", () => {
    expect(changesAddress(row)).toBe("/api/packets/7/changes?packet=3&version=9");
    expect(changesAddress(row, 4)).toBe("/api/packets/7/changes/patch?packet=3&version=9&file=4");
    expect(changesAddress(row, 4, "files")).toBe("/api/packets/7/changes/files?packet=3&version=9&file=4");
  });
});

describe("expandableFile", () => {
  test("only a text change with a version at each commit shows its unmodified lines", () => {
    const text = { text_diff: true, binary: false };
    for (const status of ["modified", "renamed", "copied"]) expect(expandableFile({ ...text, status })).toBe(true);
    for (const status of ["added", "deleted", "type-changed"]) expect(expandableFile({ ...text, status })).toBe(false);
    expect(expandableFile({ status: "modified", text_diff: false, binary: false })).toBe(false);
    expect(expandableFile({ status: "modified", text_diff: false, binary: true })).toBe(false);
    expect(expandableFile(null)).toBe(false);
  });
});

describe("comparisonState", () => {
  test("an expired packet, an absent record, and a recorded comparison differ", () => {
    expect(comparisonState(row, { expired: false })).toBe("ready");
    expect(comparisonState(row, { expired: true })).toBe("expired");
    expect(comparisonState({ ...row, candidate_changes: null }, { expired: false })).toBe("absent");
  });

  test("each state that the view cannot show has its own text", () => {
    expect(comparisonStateText("expired", { expiredAt: "Nov 10, 09:14" }))
      .toBe("The change data expired on Nov 10, 09:14. The repository keeps the commits.");
    expect(comparisonStateText("absent", {})).toBe("No merge candidate is available for this packet.");
    expect(comparisonStateText("absent", {}, row)).toBe("Culler recorded no change data for this merge candidate.");
  });
});

