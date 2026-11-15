// stages.js — the task lifecycle, in pipeline order, with the display facts
// every pane shares. One copy, read by the Board and the flow map; the order
// is dispatch's own status set, with `packaged` last because that is where
// work waits for the operator rather than for the system.
export const STAGES = ["proposed", "seeded", "worked", "verified", "packaged"];

/** The display facts of each stage. */
export const STAGE_META = {
  proposed: { description: "The task awaits selection by the Arbiter.", label: "Proposed", tone: "sky" },
  seeded: { description: "The Worker prepares the task for verification.", label: "Seeded", tone: "leaf" },
  worked: { description: "The Verifier checks the work of the Worker.", label: "Worked", tone: "moss" },
  verified: { description: "The Packager prepares the review packet.", label: "Verified", tone: "stone" },
  packaged: {
    label: "Hatched", tone: "rust",
    description: "Hatched means packaged: the task has a review packet waiting for decision.",
  },
};

/** The display label for a status, tolerating a status this build does not
 * know (an old spine's row is still a fact, not a crash). */
export function stageLabel(status) {
  return STAGE_META[status]?.label ?? String(status ?? "unknown");
}

export function stageTone(status) {
  return STAGE_META[status]?.tone ?? "stone";
}
