// Each Bun test process starts with the environment of an operator host.
// An agent container sets run variables and binds /mc/run.json, /mc/logs, and /cache.
// A test that inherits them tests the container instead of the code.
import { tmpdir } from "node:os";
import { join } from "node:path";

export const RUN_VARIABLES = [
  "CODEX_HOME", "CULLER_CACHE_LIMIT_BYTES", "MC_AGENT_RUNNER_ROUTES", "MC_ARTIFACTS_ROOT", "MC_BIN", "MC_BIN_DIR",
  "MC_CLAUDE_ADAPTER", "MC_CLAUDE_BIN", "MC_CODEX_ADAPTER", "MC_CODEX_BIN", "MC_HARNESS_CLI", "MC_HELPER",
  "MC_HOME", "MC_COLLIE_POLL_MS", "MC_LOG_LEVEL", "MC_POOL_IDS", "MC_PRIVATE_BUN",
  "MC_PRIVATE_NODE", "MC_ROLE", "MC_RUN_ID", "MC_RUN_JSON", "MC_RUN_LOG_DIR",
  "MC_SCRIBE_IMAGE_OK", "MC_SPINE", "MC_SUBJECT_ID", "MC_TASK_STORE_ROOT",
  "MC_TICK_INTERVAL_MS", "MC_TOKEN",
];

for (const name of RUN_VARIABLES) delete process.env[name];
// These paths do not exist, so the code under test finds no run identity and logs to stderr.
const absent = join(tmpdir(), `culler-test-environment-${process.pid}`);
process.env["MC_RUN_JSON"] = join(absent, "run.json");
process.env["MC_RUN_LOG_DIR"] = join(absent, "logs");
