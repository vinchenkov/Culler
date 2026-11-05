import { makeLogger, renderText } from "/private/tmp/culler-worktrees/mac-command-latency/runner/shared/log";
import { observeCommand } from "/private/tmp/culler-worktrees/mac-command-latency/dashboard/src/main";
const now = 1791756000000;
const logger = makeLogger({comp: "dashboard", level: "info", now: () => now, sink: {write(line) { process.stdout.write(renderText(JSON.parse(line)) + "\n"); }}});
const base = {startedAtUnixMs:now-100, spawnedAtUnixMs:now-99, exitObservedAtUnixMs:now, completedAtUnixMs:now, elapsedMs:100, queueMs:1, pid:12345, code:0, outcome:"completed" as const};
observeCommand(logger,base);
observeCommand(logger,{...base, code:124, outcome:"deadline"});
observeCommand(logger,{...base, code:137, outcome:"cancelled"});
observeCommand(logger,{...base, spawnedAtUnixMs:null, exitObservedAtUnixMs:null, pid:null, code:null, outcome:"error"});
