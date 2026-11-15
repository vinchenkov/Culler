import { expect, test } from "bun:test";
import { pairedTreatmentRatio, summarize, type PairedSample } from "./statistics";

test("the summary retains failures and timeouts in the duration distribution", () => {
  const result = summarize([
    { elapsedMs: 10, valid: true },
    { elapsedMs: 20, valid: true },
    { elapsedMs: 20_000, valid: false, timedOut: true },
    { elapsedMs: null, valid: false },
  ]);
  expect(result.count).toBe(4);
  expect(result.successes).toBe(2);
  expect(result.errors).toBe(2);
  expect(result.timedOut).toBe(1);
  expect(result.timedCount).toBe(3);
  expect(result.missingDurationCount).toBe(1);
  expect(result.medianMs).toBe(20);
  expect(result.p95Ms).toBeCloseTo(18_002, 8);
  expect(result.p99Ms).toBeCloseTo(19_600.4, 8);
  expect(result.maxMs).toBe(20_000);
  expect(result.overDeadline).toBe(1);
});

test("an empty summary reports no duration instead of a zero-duration success", () => {
  expect(summarize([])).toMatchObject({ count: 0, errors: 0, medianMs: null, p95Ms: null, p99Ms: null, maxMs: null });
  expect(() => summarize([{ elapsedMs: Number.NaN, valid: true }])).toThrow();
});

function pair(blockId: string, pairId: string, baseline: number, treatment: number): PairedSample[] {
  return [
    { blockId, pairId, variant: "baseline", elapsedMs: baseline, valid: true, outputDigest: "same" },
    { blockId, pairId, variant: "treatment", elapsedMs: treatment, valid: true, outputDigest: "same" },
  ];
}

test("pair identifiers prevent unrelated durations from becoming comparison pairs", () => {
  const samples: PairedSample[] = [
    ...pair("one", "a", 100, 50),
    ...pair("two", "a", 200, 100),
    { blockId: "one", pairId: "missing", variant: "baseline", elapsedMs: 1000, valid: true },
    { blockId: "two", pairId: "missing", variant: "treatment", elapsedMs: 1, valid: true },
    ...pair("three", "failure", 100, 1).map((sample) => ({ ...sample, valid: sample.variant === "baseline" })),
    ...pair("four", "different", 100, 1).map((sample) => ({ ...sample, outputDigest: sample.variant })),
  ];
  const result = pairedTreatmentRatio(samples.reverse(), "baseline", "treatment", { bootstrapSamples: 100, seed: 7 });
  expect(result).toMatchObject({ totalPairs: 6, pairedValidCount: 2, missingPairs: 2, invalidPairs: 1, outputMismatchPairs: 1 });
  expect(result.ratio).toBeCloseTo(0.5, 12);
  expect(result.interval?.low).toBeCloseTo(0.5, 12);
  expect(result.interval?.high).toBeCloseTo(0.5, 12);
});

test("the bootstrap preserves complete blocks and uses a repeatable seed", () => {
  const samples = [
    ...pair("slow", "a", 100, 200),
    ...pair("slow", "b", 100, 200),
    ...pair("fast", "a", 100, 50),
    ...pair("fast", "b", 100, 50),
  ];
  const options = { bootstrapSamples: 1000, seed: 19 };
  const first = pairedTreatmentRatio(samples, "baseline", "treatment", options);
  expect(first).toEqual(pairedTreatmentRatio([...samples].reverse(), "baseline", "treatment", options));
  expect(first.ratio).toBeCloseTo(1, 12);
  expect(first.interval).toEqual({ low: 0.5, high: 2 });
  expect(pairedTreatmentRatio(samples.slice(0, 4), "baseline", "treatment", options).interval).toBeNull();
});

test("duplicate variant samples cause a comparison failure", () => {
  const samples = pair("block", "pair", 100, 50);
  expect(() => pairedTreatmentRatio([...samples, samples[0]!], "baseline", "treatment")).toThrow("duplicate");
});
