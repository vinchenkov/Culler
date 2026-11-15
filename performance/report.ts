import { createHash } from "node:crypto";
import { lstat, mkdir, readFile, writeFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { pairedTreatmentRatio, summarize, type DurationSample, type PairedSample } from "./statistics";

interface Trial extends DurationSample {
  id: string;
  variant: string;
  argv: string[];
  blockId: string | null;
  pairId: string | null;
  warmup: boolean;
  outputDigest: string | null;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function argumentsFrom(argv: string[]): Map<string, string> {
  const result = new Map<string, string>();
  for (let index = 0; index < argv.length; index++) {
    const key = argv[index]!;
    if (key === "--allow-incomplete") {
      if (result.has(key)) throw new Error("The --allow-incomplete argument must appear only once.");
      result.set(key, "true");
      continue;
    }
    if (!key.startsWith("--") && !result.has("--input")) {
      result.set("--input", key);
      continue;
    }
    if (!["--input", "--output", "--baseline", "--treatment", "--bootstrap-samples", "--seed"].includes(key)) {
      throw new Error(`The argument ${key} is unknown.`);
    }
    const value = argv[++index];
    if (value === undefined || value.startsWith("--") || result.has(key)) {
      throw new Error(`The argument ${key} needs one value.`);
    }
    result.set(key, value);
  }
  return result;
}

function identifier(value: unknown): string | null {
  return typeof value === "string" || typeof value === "number" ? String(value) : null;
}

function parseTrial(value: unknown, line: number): Trial | null {
  if (!isRecord(value)) throw new Error(`Line ${line} must contain a JSON object.`);
  const trialType = ["trial", "result", "trial-timeout", "worker-error"].includes(String(value.type));
  if (!trialType && !(typeof value.variant === "string" && Array.isArray(value.argv))) return null;
  if (typeof value.variant !== "string" || value.variant.length === 0 ||
      !Array.isArray(value.argv) || !value.argv.every((part) => typeof part === "string")) {
    throw new Error(`Trial line ${line} needs a variant and a command argument list.`);
  }
  const elapsedMs = value.elapsedMs ?? null;
  if (elapsedMs !== null && (typeof elapsedMs !== "number" || !Number.isFinite(elapsedMs) || elapsedMs < 0)) {
    throw new Error(`Trial line ${line} contains an invalid duration.`);
  }
  return {
    id: identifier(value.id) ?? `line-${line}`,
    variant: value.variant,
    argv: value.argv as string[],
    elapsedMs: elapsedMs as number | null,
    valid: value.valid === true,
    timedOut: value.timedOut === true,
    blockId: identifier(value.blockId),
    pairId: identifier(value.pairId),
    warmup: value.warmup === true || value.phase === "warmup",
    outputDigest: typeof value.outputDigest === "string" ? value.outputDigest : null,
  };
}

function commandKey(argv: string[]): string {
  return JSON.stringify(argv);
}

function cell(value: string): string {
  return value.replaceAll("|", "\\|").replace(/[\r\n]+/g, " ");
}

function number(value: number | null, digits = 2): string {
  return value === null ? "unavailable" : value.toFixed(digits);
}

function help(): void {
  console.log([
    "Create benchmark reports.",
    "",
    "Usage: report.ts --input <path>",
    "                 --output <path.json>",
    "",
    "Options:",
    "  --input <path>",
    "    Read trial records from this file.",
    "  --output <path.json>",
    "    Create JavaScript Object Notation",
    "    (JSON) and Markdown reports.",
    "    Existing files remain intact.",
    "  --baseline <name>",
    "    Compare this variant with others.",
    "  --treatment <name>",
    "    Select one comparison variant.",
    "  --bootstrap-samples <count>",
    "    Use this count. Default: 5000.",
    "  --seed <integer>",
    "    Use this seed. Default: 1129663564.",
    "  --allow-incomplete",
    "    Report partial evidence explicitly.",
    "  --help",
    "    Show this help.",
  ].join("\n"));
}

async function requireAbsent(path: string): Promise<void> {
  try {
    await lstat(path);
  } catch (error) {
    if (isRecord(error) && error.code === "ENOENT") return;
    throw error;
  }
  throw new Error("An output file exists. Choose another --output path.");
}

async function optionalRecord(path: string): Promise<Record<string, unknown> | null> {
  let text: string;
  try {
    text = await readFile(path, "utf8");
  } catch (error) {
    if (isRecord(error) && error.code === "ENOENT") return null;
    throw error;
  }
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    throw new Error(`The run record contains invalid JSON: ${path}`);
  }
  if (!isRecord(value)) throw new Error(`The run record must contain a JSON object: ${path}`);
  return value;
}

async function runStatus(inputPath: string, count: number, hasControllerTrials: boolean, allowIncomplete: boolean) {
  const completionPath = resolve(dirname(inputPath), "completion.json");
  const metadataPath = resolve(dirname(inputPath), "metadata.json");
  const completion = await optionalRecord(completionPath);
  const metadata = await optionalRecord(metadataPath);
  const controllerRun = hasControllerTrials || metadata !== null;
  const reasons: string[] = [];
  let state: "complete" | "incomplete" | "unknown" = completion === null ? "unknown" : "complete";
  let expectedTrials: number | null = null;
  if (metadata !== null && typeof metadata.trialsPerVariant === "number" &&
      Number.isInteger(metadata.trialsPerVariant) && metadata.trialsPerVariant > 0 && Array.isArray(metadata.variants)) {
    expectedTrials = metadata.trialsPerVariant * metadata.variants.length;
  }
  if (completion === null) {
    reasons.push("The input has no controller completion record.");
    if (controllerRun) state = "incomplete";
  } else {
    if (completion.outcome !== "complete") {
      state = "incomplete";
      reasons.push("The controller did not report a complete run.");
    }
    if (completion.completedTrials !== count) {
      state = "incomplete";
      reasons.push("The completion count differs from the input trial count.");
    }
  }
  if (expectedTrials !== null && expectedTrials !== count) {
    state = "incomplete";
    reasons.push("The input trial count differs from the planned trial count.");
  }
  if (state === "incomplete" && !allowIncomplete) {
    throw new Error("The controller run is incomplete. Use --allow-incomplete to report the partial evidence.");
  }
  return {
    state,
    controllerRun,
    completionPath: completion === null ? null : completionPath,
    metadataPath: metadata === null ? null : metadataPath,
    outcome: completion?.outcome ?? null,
    completedTrials: completion?.completedTrials ?? null,
    expectedTrials,
    allowIncomplete,
    reasons,
  };
}

async function main(): Promise<void> {
  if (process.argv.slice(2).includes("--help")) {
    help();
    return;
  }
  const args = argumentsFrom(process.argv.slice(2));
  if (!args.has("--input") || !args.has("--output")) {
    throw new Error("The report needs --input and --output paths.");
  }
  if (args.has("--treatment") && !args.has("--baseline")) {
    throw new Error("A treatment comparison needs an explicit baseline name.");
  }
  const inputPath = resolve(args.get("--input")!);
  const outputPath = resolve(args.get("--output")!);
  if (!outputPath.endsWith(".json")) throw new Error("The output path must end with .json.");
  const markdownPath = outputPath.slice(0, -5) + ".md";
  if (inputPath === outputPath || inputPath === markdownPath) {
    throw new Error("The output path must differ from the input path.");
  }
  await requireAbsent(outputPath);
  await requireAbsent(markdownPath);
  const raw = await readFile(inputPath, "utf8");
  const trials: Trial[] = [];
  let metadataRecords = 0;
  let hasControllerTrials = false;
  for (const [index, line] of raw.split(/\r?\n/).entries()) {
    if (line.trim() === "") continue;
    let value: unknown;
    try {
      value = JSON.parse(line);
    } catch {
      throw new Error(`Line ${index + 1} contains invalid JSON.`);
    }
    const trial = parseTrial(value, index + 1);
    if (trial === null) metadataRecords++;
    else {
      trials.push(trial);
      if (isRecord(value) && value.type === "trial" && "controllerObservedAt" in value) hasControllerTrials = true;
    }
  }
  if (trials.length === 0) throw new Error("The input contains no trial records.");
  const measured = trials.filter((trial) => !trial.warmup);
  if (measured.length === 0) throw new Error("The input contains warmups but no measured trials.");
  const status = await runStatus(inputPath, measured.length, hasControllerTrials, args.has("--allow-incomplete"));
  const variants = [...new Set(measured.map((trial) => trial.variant))].sort();
  const commands = [...new Set(measured.map((trial) => commandKey(trial.argv)))].sort();
  const byVariant = variants.map((variant) => ({
    variant,
    summary: summarize(measured.filter((trial) => trial.variant === variant)),
    commands: commands.map((key) => ({
      argv: JSON.parse(key) as string[],
      summary: summarize(measured.filter((trial) => trial.variant === variant && commandKey(trial.argv) === key)),
    })).filter((command) => command.summary.count > 0),
  }));

  const baseline = args.get("--baseline") ?? null;
  if (baseline !== null && !variants.includes(baseline)) throw new Error("The input contains no measured baseline trials.");
  const treatment = args.get("--treatment");
  if (treatment !== undefined && (!variants.includes(treatment) || treatment === baseline)) {
    throw new Error("The treatment must name a measured variant other than the baseline.");
  }
  const comparisons = [];
  if (baseline !== null) {
    for (const candidate of treatment === undefined ? variants.filter((variant) => variant !== baseline) : [treatment]) {
      for (const key of commands) {
        const selected = measured.filter((trial) => commandKey(trial.argv) === key &&
          (trial.variant === baseline || trial.variant === candidate));
        const paired: PairedSample[] = selected.filter((trial) => trial.blockId !== null && trial.pairId !== null)
          .map((trial) => ({ ...trial, blockId: trial.blockId!, pairId: trial.pairId! }));
        comparisons.push({
          argv: JSON.parse(key) as string[],
          unpairedRecords: selected.length - paired.length,
          ...pairedTreatmentRatio(paired, baseline, candidate, {
            bootstrapSamples: Number(args.get("--bootstrap-samples") ?? "5000"),
            seed: Number(args.get("--seed") ?? "1129663564"),
          }),
        });
      }
    }
  }
  const notes = [
    "Only trials explicitly marked as warmups are excluded from duration summaries.",
    "Failed trials with measured durations remain in every duration summary.",
    "Trials without durations count as errors when their valid field is false; they do not enter duration quantiles.",
    "Timeout durations are lower bounds when the controller stops a command before completion.",
    "Paired ratios require valid, positive durations and matching output digests when both digests exist.",
    "The comparison reports missing, invalid, and different-output pairs separately.",
    "The bootstrap resamples blocks and needs at least two blocks with valid pairs for a confidence interval.",
    "The completeBlocks field counts blocks with at least one valid pair.",
    "A ratio below one means the treatment has a lower geometric mean duration.",
    "Confidence intervals do not remove bias from different host conditions between variants.",
  ];
  const report = {
    schemaVersion: 1,
    createdAt: new Date().toISOString(),
    inputPath,
    inputSHA256: createHash("sha256").update(raw).digest("hex"),
    totalTrials: trials.length,
    measuredTrials: measured.length,
    excludedWarmups: trials.length - measured.length,
    metadataRecords,
    runStatus: status,
    byVariant,
    comparisons,
    notes,
  };
  const markdown = [
    "# Command duration report", "",
    status.state === "incomplete" ? "**Run status: incomplete. These results describe partial evidence.**" :
      status.state === "unknown" ? "**Run status: unknown. This input has no controller completion record.**" :
        "**Run status: complete.**", "",
    `The report contains ${measured.length} measured trials and excludes ${report.excludedWarmups} declared warmups.`, "",
    "All durations use milliseconds. Failed trials remain in the summaries.", "",
    "| Variant | Command | Count | Errors | Timeouts | Missing durations | Median | 95th percentile | 99th percentile | Maximum | Above 15 seconds |",
    "| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ...byVariant.flatMap((group) => group.commands.map(({ argv, summary }) =>
      `| ${cell(group.variant)} | ${cell(argv.join(" "))} | ${summary.count} | ${summary.errors} | ${summary.timedOut} | ${summary.missingDurationCount} | ${number(summary.medianMs)} | ${number(summary.p95Ms)} | ${number(summary.p99Ms)} | ${number(summary.maxMs)} | ${summary.overDeadline} |`)),
    "", "## Paired comparisons", "",
    ...(comparisons.length === 0 ? ["No paired comparison was requested."] : [
      "| Baseline | Treatment | Command | Valid pairs | Blocks with valid pairs | Ratio | 95% interval | Missing pairs | Invalid pairs | Different output | Unpaired records |",
      "| --- | --- | --- | ---: | ---: | ---: | --- | ---: | ---: | ---: | ---: |",
      ...comparisons.map((comparison) =>
        `| ${cell(comparison.baselineVariant)} | ${cell(comparison.treatmentVariant)} | ${cell(comparison.argv.join(" "))} | ${comparison.pairedValidCount} | ${comparison.completeBlocks} | ${number(comparison.ratio, 4)} | ${comparison.interval === null ? "unavailable" : `${number(comparison.interval.low, 4)}–${number(comparison.interval.high, 4)}`} | ${comparison.missingPairs} | ${comparison.invalidPairs} | ${comparison.outputMismatchPairs} | ${comparison.unpairedRecords} |`),
    ]),
    "", "## Method", "", ...notes.map((note) => `- ${note}`), "",
    `Input digest, Secure Hash Algorithm 256 (SHA-256): \`${report.inputSHA256}\`.`, "",
  ].join("\n");
  await mkdir(dirname(outputPath), { recursive: true });
  await writeFile(outputPath, JSON.stringify(report, null, 2) + "\n", { flag: "wx" });
  await writeFile(markdownPath, markdown, { flag: "wx" });
  console.log(JSON.stringify({ type: "report", outputPath, markdownPath, measuredTrials: measured.length, runStatus: status.state }));
}

try {
  await main();
} catch (error) {
  console.error(JSON.stringify({ type: "error", operation: "report",
    error: error instanceof Error ? error.message : "The report could not complete." }));
  process.exitCode = 1;
}
