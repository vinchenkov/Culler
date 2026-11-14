import { expect, test } from "bun:test";
import { assessmentProblem, blankAssessment } from "./evaluation.js";

test("the first assessment has no inferred scores", () => {
 const draft = blankAssessment({ packet_id: 1 }, "rubric-hash");
 expect(draft.scores).toEqual({});
 expect(draft.critical_failure).toBeNull();
 expect(draft.source_inspected).toBeNull();
 expect(draft.evidence_inspected).toBeNull();
 expect(assessmentProblem(draft)).toBe("Select all six scores.");
});
test("the form rejects incomplete and fractional scores", () => {
 const draft = blankAssessment({ packet_id: 1 }, "rubric-hash");
 for (const key of ["comprehension", "causal_explanation", "decision_support", "visual_explanation", "clarity", "packet_length"]) draft.scores[key] = 3;
 expect(assessmentProblem(draft)).toContain("critical failure");
 draft.critical_failure = false;
 draft.reason = "The evidence supports the explanation.";
 draft.familiar_project = true;
 draft.source_inspected = true;
 draft.evidence_inspected = false;
 expect(assessmentProblem(draft)).toBe("");
 for (const score of [0.5, -1, 5, null]) { draft.scores.clarity = score; expect(assessmentProblem(draft)).not.toBe(""); }
});
