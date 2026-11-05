export interface DurationSample {
  elapsedMs: number | null;
  valid: boolean;
  timedOut?: boolean;
}

export interface PairedSample extends DurationSample {
  blockId: string;
  pairId: string;
  variant: string;
  outputDigest?: string | null;
}

function duration(sample: DurationSample): number | null {
  if (sample.elapsedMs === null) return null;
  if (!Number.isFinite(sample.elapsedMs) || sample.elapsedMs < 0) {
    throw new Error("A duration must be a finite, nonnegative number or null.");
  }
  return sample.elapsedMs;
}

function quantile(sorted: number[], probability: number): number | null {
  if (sorted.length === 0) return null;
  const position = (sorted.length - 1) * probability;
  const lower = Math.floor(position);
  const upper = Math.ceil(position);
  return sorted[lower]! + (sorted[upper]! - sorted[lower]!) * (position - lower);
}

export function summarize(samples: DurationSample[], deadlineMs = 15_000) {
  if (!Number.isFinite(deadlineMs) || deadlineMs < 0) {
    throw new Error("The deadline must be a finite, nonnegative number.");
  }
  const values = samples.map(duration).filter((value): value is number => value !== null);
  values.sort((a, b) => a - b);
  return {
    count: samples.length,
    timedCount: values.length,
    successes: samples.filter((sample) => sample.valid && !sample.timedOut).length,
    errors: samples.filter((sample) => !sample.valid || sample.timedOut).length,
    timedOut: samples.filter((sample) => sample.timedOut).length,
    missingDurationCount: samples.length - values.length,
    medianMs: quantile(values, 0.5),
    p95Ms: quantile(values, 0.95),
    p99Ms: quantile(values, 0.99),
    maxMs: values.at(-1) ?? null,
    overDeadline: values.filter((value) => value > deadlineMs).length,
    deadlineMs,
    quantileMethod: "linear interpolation" as const,
  };
}

export interface BootstrapOptions {
  bootstrapSamples?: number;
  seed?: number;
  confidence?: number;
}

function randomSource(seed: number): () => number {
  let state = seed >>> 0 || 0x9e3779b9;
  return () => {
    state ^= state << 13;
    state ^= state >>> 17;
    state ^= state << 5;
    return (state >>> 0) / 0x1_0000_0000;
  };
}

// Resample complete blocks to preserve dependence between nearby command trials.
export function pairedTreatmentRatio(
  samples: PairedSample[],
  baselineVariant: string,
  treatmentVariant: string,
  options: BootstrapOptions = {},
) {
  if (baselineVariant === treatmentVariant) {
    throw new Error("The baseline and treatment must have different names.");
  }
  const bootstrapSamples = options.bootstrapSamples ?? 5_000;
  const seed = options.seed ?? 0x43554c4c;
  const confidence = options.confidence ?? 0.95;
  if (!Number.isInteger(bootstrapSamples) || bootstrapSamples < 1) {
    throw new Error("The bootstrap sample count must be a positive integer.");
  }
  if (!Number.isInteger(seed) || seed < 0 || seed > 0xffff_ffff) {
    throw new Error("The seed must be an unsigned 32-bit integer.");
  }
  if (!Number.isFinite(confidence) || confidence <= 0 || confidence >= 1) {
    throw new Error("The confidence level must be greater than zero and less than one.");
  }

  const pairs = new Map<string, { blockId: string; baseline?: PairedSample; treatment?: PairedSample }>();
  for (const sample of samples) {
    if (sample.variant !== baselineVariant && sample.variant !== treatmentVariant) continue;
    duration(sample);
    const key = JSON.stringify([sample.blockId, sample.pairId]);
    const pair = pairs.get(key) ?? { blockId: sample.blockId };
    const side = sample.variant === baselineVariant ? "baseline" : "treatment";
    if (pair[side] !== undefined) {
      throw new Error("A pair contains duplicate samples for one variant.");
    }
    pair[side] = sample;
    pairs.set(key, pair);
  }

  const blocks = new Map<string, number[]>();
  let missingPairs = 0;
  let invalidPairs = 0;
  let outputMismatchPairs = 0;
  for (const [, pair] of [...pairs].sort(([a], [b]) => a.localeCompare(b))) {
    const { baseline, treatment } = pair;
    if (baseline === undefined || treatment === undefined) {
      missingPairs++;
      continue;
    }
    if (!baseline.valid || !treatment.valid || baseline.timedOut || treatment.timedOut ||
        baseline.elapsedMs === null || treatment.elapsedMs === null ||
        baseline.elapsedMs <= 0 || treatment.elapsedMs <= 0) {
      invalidPairs++;
      continue;
    }
    if (baseline.outputDigest != null && treatment.outputDigest != null &&
        baseline.outputDigest !== treatment.outputDigest) {
      outputMismatchPairs++;
      continue;
    }
    const values = blocks.get(pair.blockId) ?? [];
    values.push(Math.log(treatment.elapsedMs / baseline.elapsedMs));
    blocks.set(pair.blockId, values);
  }

  const blockValues = [...blocks].sort(([a], [b]) => a.localeCompare(b)).map(([, values]) => values);
  const logs = blockValues.flat();
  const ratio = logs.length === 0 ? null : Math.exp(logs.reduce((sum, value) => sum + value, 0) / logs.length);
  let interval: { low: number; high: number } | null = null;
  if (blockValues.length >= 2) {
    const random = randomSource(seed);
    const estimates: number[] = [];
    for (let iteration = 0; iteration < bootstrapSamples; iteration++) {
      let sum = 0;
      let count = 0;
      for (let index = 0; index < blockValues.length; index++) {
        const block = blockValues[Math.floor(random() * blockValues.length)]!;
        for (const value of block) sum += value;
        count += block.length;
      }
      estimates.push(Math.exp(sum / count));
    }
    estimates.sort((a, b) => a - b);
    const tail = (1 - confidence) / 2;
    interval = { low: quantile(estimates, tail)!, high: quantile(estimates, 1 - tail)! };
  }
  return {
    baselineVariant,
    treatmentVariant,
    totalPairs: pairs.size,
    pairedValidCount: logs.length,
    completeBlocks: blockValues.length,
    missingPairs,
    invalidPairs,
    outputMismatchPairs,
    ratio,
    interval,
    confidence,
    bootstrapSamples,
    seed,
    method: "paired geometric mean ratio; percentile bootstrap over blocks" as const,
  };
}
