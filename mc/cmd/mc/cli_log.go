package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"mc/mclog"
	"mc/verbs"
)

// cli_log.go — the mc binary's own record of what it was asked to do.
//
// Before this, an invocation left nothing behind: 53 diagnostic sites all wrote
// free prose to the caller's stderr and the caller was usually a resident tick
// that threw it away. Every mutation of the spine passed through this process
// and none of them were reconstructable an hour later.
//
// Three properties make this safe to have on by default:
//
//   - The logger is constructed HERE and nowhere else. Nothing under verbs/,
//     domain/, substrate/, dispatch/, or deployment/ emits, so the verb layer
//     stays a pure function of its inputs and the "one JSON object on stdout"
//     contract is untouched.
//   - A nil *Logger is a working logger whose methods do nothing, and
//     mclog.OpenComponent returns nil whenever $MC_HOME/logs is not visible.
//     Inside the helper container and inside every agent container it is not
//     visible, so a self-delegated verb is recorded exactly once, host-side,
//     with no branch anywhere.
//   - Only a token SHAPED LIKE A NAME is recorded. A verb name, its subverb,
//     and the sorted names of the flags it carried are the whole shape; free
//     operator text, task ids, run ids, refs, and file paths all carry
//     a digit, a slash, a dot, a space, or a capital, so none of them can pass
//     for one. Flag VALUES are never read at all.

// cliLog is the process-wide logger, opened once by run() and read by
// writeVerbError. It is deliberately a package variable rather than a threaded
// parameter: writeVerbError is reached from dozens of call sites across a dozen
// files, and a plumbing change that wide is a worse risk than this one.
// Untouched it is nil, and nil logs nothing.
var cliLog *mclog.Logger

// openCLILogger opens $MC_HOME/logs/mc.ndjson, or gives back a nil logger. An
// unresolvable MC_HOME is not an error here: mc must run identically whether or
// not a deployment exists, and a CLI that refuses to work because it could not
// open its log is worse than one that keeps no log.
func openCLILogger() *mclog.Logger {
	home, err := configuredCanonicalHome()
	if err != nil {
		return nil
	}
	level := mclog.ParseLevel(os.Getenv("MC_LOG_LEVEL"), mclog.LevelInfo)
	lg, err := mclog.OpenComponent(filepath.Join(home, "logs"), "mc", level)
	if err != nil {
		return nil
	}
	return lg
}

// cliReadVerbs names the invocations that only read. They log at debug so a
// deployment at the default level records what CHANGED, and turning the knob to
// debug turns the same file into a full trace of the traffic. Anything absent
// from this map logs at info: an unclassified verb is assumed to mutate, which
// is the direction that loses nothing.
//
// The key is the verb when every one of its subverbs reads, or "verb sub" when
// only some do; both spellings are looked up. `doctor` is deliberately absent —
// it materializes the warm helper as a side effect, so it is not a read.
var cliReadVerbs = map[string]bool{
	"config":                    true,
	"__database-schema":         true,
	"__redeploy-check":          true,
	"routing":                   true,
	"logs":                      true,
	"health":                    true,
	"activity list":             true,
	"board history-report":      true,
	"task get":                  true,
	"task list":                 true,
	"packet list":               true,
	"collie list":               true,
	"collie history":            true,
	"collie packet-context":     true,
	"project list":              true,
	"attachment ingress-queued": true,
	"outbox poll":               true,
	"lease list":                true,
	"wiki-state list":           true,
	"pause status":              true,
	"settings get":              true,
	"__trace-sessions":          true,
	"__project-image-digests":   true,
	"__agentsview-access":       true,
}

// verbToken is the shape of a name in the verb surface: lowercase letters and
// hyphens, with the internal verbs prefixed by underscores. Nothing else may be
// recorded, because everything else is a VALUE. A task id, a run id, an
// absolute path, a ref, and free operator text each carry a digit, a slash, a
// dot, an underscore in the middle, or a capital, so the shape alone excludes
// them — no redaction list to keep in step with the surface, and no way for a
// new verb that takes a positional to quietly start logging its argument.
//
// This is not hypothetical: reading the first non-flag token as the subverb
// wrote `mc complete 1` as sub=1 and `mc __mount-recheck <path>` as the whole
// absolute path.
var verbToken = regexp.MustCompile(`^_{0,2}[a-z][a-z-]{0,31}$`)

// flagToken is the same rule for flag names, which may carry a digit. A flag
// name follows a leading dash, so it cannot be a bare positional value, but an
// unshaped one is still dropped rather than written.
var flagToken = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// invocationShape reduces argv to the parts that are safe and useful to keep:
// the verb, the subverb when there is one, and the sorted flag names. Values
// are dropped, not redacted — nothing here can leak because nothing that fails
// verbToken is written at all.
func invocationShape(args []string) (verb, sub, flags string) {
	verb = args[0]
	if !verbToken.MatchString(verb) && !strings.HasPrefix(verb, "-") {
		// An unknown first token is still worth one record — that a verb was
		// refused is the fact — but it is the operator's text, not a name.
		verb = "(value)"
	}
	rest := args[1:]
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") && verbToken.MatchString(rest[0]) {
		sub = rest[0]
	}
	names := []string{}
	seen := map[string]bool{}
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") || a == "-" || a == "--" {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		if i := strings.IndexByte(name, '='); i >= 0 {
			name = name[:i]
		}
		if name == "" || seen[name] || !flagToken.MatchString(name) {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	sort.Strings(names)
	return verb, sub, strings.Join(names, ",")
}

// cliScope reports the caller's provenance the same way RequireHostScope
// derives it — run.json presence, never a flag. An unreadable run.json is
// reported as "unknown" rather than guessed at.
func cliScope() string {
	id, err := verbs.LoadIdentity()
	if err != nil {
		return "unknown"
	}
	if id == nil {
		return "host"
	}
	if id.Tier != "" {
		return id.Tier
	}
	return "run"
}

// invocationKey is the verb plus its subverb, the same string cliReadVerbs is
// keyed by and the same string that lands in msg.
func invocationKey(verb, sub string) string {
	if sub == "" {
		return verb
	}
	return verb + " " + sub
}

// logInvocation writes the one record per invocation. The duration is measured
// around the whole dispatch, delegation included, so a slow verb and a slow
// helper are not separable here — that separation would need instrumentation of
// the crossing itself, which is not what this record is for.
func logInvocation(args []string, exit int, started time.Time) {
	if cliLog == nil {
		return
	}
	verb, sub, flags := invocationShape(args)
	ctx := mclog.Ctx{
		"pid":    os.Getpid(),
		"verb":   verb,
		"argc":   len(args),
		"exit":   exit,
		"dur_ms": time.Since(started).Milliseconds(),
		"scope":  cliScope(),
	}
	if sub != "" {
		ctx["sub"] = sub
	}
	if flags != "" {
		ctx["flags"] = flags
	}
	key := invocationKey(verb, sub)
	level := mclog.LevelInfo
	if cliReadVerbs[key] || cliReadVerbs[verb] {
		level = mclog.LevelDebug
	}
	cliLog.At(level, "cli.invoked", "mc "+key, ctx)
}
