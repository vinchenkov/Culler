import type { Exec } from "./types";
import type { Logger } from "../../runner/shared/log";

export async function applyPendingVisions(runMc: Exec, log: Logger): Promise<void> {
  try {
    const result = await runMc(["project", "vision-pending"]);
    if (result.exitCode !== 0) throw new Error("The Vision submissions are unavailable.");
    const { submissions } = JSON.parse(result.stdout);
    if (!Array.isArray(submissions) || !submissions.every((item) =>
      item && typeof item.id === "string" && typeof item.submission_id === "string")) {
      throw new Error("The Vision submissions are invalid.");
    }
    for (const submission of submissions) {
      try {
        const applied = await runMc(["project", "vision-apply"],
          new TextEncoder().encode(JSON.stringify({ id: submission.id, submission_id: submission.submission_id })));
        if (applied.exitCode !== 0) log.warn("vision.save_failed", "Culler cannot complete the Vision save.");
      } catch {
        log.warn("vision.save_failed", "Culler cannot complete the Vision save.");
      }
    }
  } catch {
    log.warn("vision.queue_failed", "Culler cannot inspect the Vision submissions.");
  }
}
