package verbs

// health.go — the deployment's health as host FILES, deliberately without the
// spine and without Docker.
//
// That constraint is the point. Every other health surface this system has
// needs something to be working: `mc doctor` materializes the warm helper and
// reads the spine, the Pipeline page reads spine rows, the Activity page reads
// the activity table. The two-day provider-quota failure loop that motivated
// this verb was invisible on all of them — `mc doctor` answered `ok: true`
// throughout — because the fault never reached a row. A health read that
// cannot answer while the spine is unreachable is a health read that goes
// silent exactly when it is needed.
//
// So this verb opens no database and spawns no container. It reads the tick
// receipt the resident writes, and it reads the log plane the components own.
// It runs host-side for the reason `mc logs` does: inside the helper $MC_HOME
// is /home/mc-model/... and every answer here would be a confident lie.
//
// The finding shape is `mc doctor`'s on purpose — check, status, detail, with
// status three-valued. The dashboard already renders that shape, so this verb
// needs no second renderer and cannot drift into one.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"mc/mclog"
)

// healthAttentionMax bounds what one call can return. A component in a tight
// failure loop writes thousands of error records a minute, and an unbounded
// `attention` array would make the single worst moment the hardest one to read.
const healthAttentionMax = 40

// tickReceiptStaleAfter is how long the resident's last completed tick may be
// in the past before this verb calls it stale.
//
// It is a constant rather than a multiple of the configured tick interval
// because this verb reads no configuration by design — it must answer when
// every other plane is unreachable, and the resident's interval lives in the
// resident's own config. Generous on purpose: the default interval is one
// minute, so five missed ticks is a resident that has genuinely stopped, not
// one that is merely busy.
const tickReceiptStaleAfter = 5 * time.Minute

// healthLogUnits are the components whose structured channel this verb judges.
// Same names as `mc logs`, and deliberately the `events` stream only: the
// launchd redirects hold free prose with no level to count.
var healthLogUnits = []string{"resident", "dashboard", "mc"}

type healthFinding struct {
	Check  string `json:"check"`
	Status string `json:"status"` // ok | fail | deferred
	Detail string `json:"detail"`
}

// Health reads the host plane and reports what it finds. `tail` is the record
// window each component's log is judged over.
func Health(tail int) (any, error) {
	tail = clampTail(tail)
	home, err := mcHomeDir()
	if err != nil {
		return nil, err
	}
	logsDir := filepath.Join(home, "logs")

	findings := []healthFinding{tickReceiptFinding(filepath.Join(home, "health", "resident-tick.json"))}
	attention := []attentionRecord{}
	for _, unit := range healthLogUnits {
		finding, records := logPlaneFinding(logsDir, unit, tail)
		findings = append(findings, finding)
		attention = append(attention, records...)
	}
	findings = append(findings, runLogFinding(logsDir))

	// Newest last, so a terminal tail and this array read the same way round.
	sort.SliceStable(attention, func(i, j int) bool { return attention[i].ts < attention[j].ts })
	if len(attention) > healthAttentionMax {
		attention = attention[len(attention)-healthAttentionMax:]
	}
	lines := make([]string, 0, len(attention))
	for _, record := range attention {
		lines = append(lines, record.text)
	}

	availability := ResidentAvailability(home, time.Now())
	ok := availability.Available && availability.CanCommit
	return map[string]any{
		"ok":           ok,
		"availability": availability,
		"setup":        residentSetupHealth(home, availability, time.Now()),
		"log_evidence": "Historical records from the selected log window.",
		"tail":         tail,
		"findings":     findings,
		"attention":    lines,
	}, nil
}

// tickReceiptFinding reads the receipt the resident renames into place after
// every completed tick. It is read here, never widened: the receipt is decoded
// elsewhere with DisallowUnknownFields, so one new field would break supervision
// activation on the next poll.
func tickReceiptFinding(path string) healthFinding {
	finding := healthFinding{Check: "resident-tick"}
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Not a fault: a deployment that has never been activated has no
			// receipt, and calling that a failure would make a fresh install
			// look broken.
			finding.Status = "deferred"
			finding.Detail = "no tick receipt yet — the resident has not completed a tick under supervision"
			return finding
		}
		finding.Status = "fail"
		finding.Detail = "tick receipt unreadable: " + err.Error()
		return finding
	}
	var receipt struct {
		Sequence    int64  `json:"sequence"`
		CompletedAt string `json:"completed_at"`
	}
	if err := json.Unmarshal(body, &receipt); err != nil {
		finding.Status = "fail"
		finding.Detail = "tick receipt is not the expected shape: " + err.Error()
		return finding
	}
	completed, err := time.Parse(time.RFC3339Nano, receipt.CompletedAt)
	if err != nil {
		finding.Status = "fail"
		finding.Detail = fmt.Sprintf("tick receipt carries no readable completed_at (%q)", receipt.CompletedAt)
		return finding
	}
	age := time.Since(completed)
	if age > tickReceiptStaleAfter {
		finding.Status = "fail"
		finding.Detail = fmt.Sprintf("last tick %d completed %s ago — the resident has stopped ticking",
			receipt.Sequence, age.Round(time.Second))
		return finding
	}
	finding.Status = "ok"
	finding.Detail = fmt.Sprintf("tick %d completed %s ago", receipt.Sequence, age.Round(time.Second))
	return finding
}

type attentionRecord struct {
	ts   string
	text string
}

// logPlaneFinding judges one component's structured channel over the tail
// window and hands back the records an operator should see.
//
// An error record is a failure BY THE AUTHOR'S OWN DECLARATION. The level is a
// literal chosen at the call site, so this function derives nothing: it counts
// what the writer already said. There is no mapping here from a code, an exit
// status, or a message to a severity, and there must never be one.
func logPlaneFinding(logsDir, unit string, tail int) (healthFinding, []attentionRecord) {
	finding := healthFinding{Check: "log-" + unit}
	path := filepath.Join(logsDir, unit+".ndjson")
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			finding.Status = "deferred"
			finding.Detail = "no structured log yet — this component has not written one"
			return finding, nil
		}
		finding.Status = "fail"
		finding.Detail = "log unreadable: " + err.Error()
		return finding, nil
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		finding.Status = "fail"
		finding.Detail = "log is not a regular file"
		return finding, nil
	}

	window, err := readLogWindow(path, unit+" events", true, "json", tail, map[string]any{})
	if err != nil {
		finding.Status = "fail"
		finding.Detail = "log window unreadable: " + err.Error()
		return finding, nil
	}
	result := window.(map[string]any)
	records, _ := result["records"].([]any)

	errors, warns := 0, 0
	newest := ""
	attention := []attentionRecord{}
	for _, entry := range records {
		record, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		ts, _ := record["ts"].(string)
		if ts > newest {
			newest = ts
		}
		level, _ := record["level"].(string)
		if level != "error" && level != "warn" {
			continue
		}
		if level == "error" {
			errors++
		} else {
			warns++
		}
		line, err := json.Marshal(record)
		if err != nil {
			continue
		}
		if text, ok := mclog.RenderText(line); ok {
			attention = append(attention, attentionRecord{ts: ts, text: text})
		}
	}

	generations := rotatedGenerations(path)
	detail := fmt.Sprintf("%d record(s) in the last %d, %d error(s), %d warning(s); %d bytes",
		len(records), tail, errors, warns, info.Size())
	if generations > 0 {
		detail += fmt.Sprintf(", %d rotated generation(s)", generations)
	}
	if newest != "" {
		detail += ", newest " + newest
	}
	if malformed, ok := result["malformed"].(int); ok && malformed > 0 {
		detail += fmt.Sprintf(", %d line(s) that are not records", malformed)
	}
	finding.Detail = detail
	if errors > 0 {
		finding.Status = "fail"
	} else {
		finding.Status = "ok"
	}
	return finding, attention
}

// rotatedGenerations counts the gzipped generations beside a live log. It is a
// health signal in its own direction: a component that has rotated is a
// component whose log is being kept bounded, and a count that stops growing on
// a busy deployment is the rotation lock wedged.
func rotatedGenerations(path string) int {
	count := 0
	for i := 1; ; i++ {
		if _, err := os.Lstat(fmt.Sprintf("%s.%d.gz", path, i)); err != nil {
			return count
		}
		count++
	}
}

// runLogFinding reports the per-run diagnostics tree. It is never a failure —
// the tree draining slowly is the prune chore working, not failing — but the
// count is the one number that says whether container diagnostics are being
// captured at all.
func runLogFinding(logsDir string) healthFinding {
	finding := healthFinding{Check: "run-logs"}
	entries, err := os.ReadDir(filepath.Join(logsDir, "runs"))
	if err != nil {
		if os.IsNotExist(err) {
			finding.Status = "deferred"
			finding.Detail = "no run diagnostics yet — no run has been dispatched under the log bind"
			return finding
		}
		finding.Status = "fail"
		finding.Detail = "run log tree unreadable: " + err.Error()
		return finding
	}
	runs := 0
	for _, entry := range entries {
		if entry.IsDir() && logsRunID.MatchString(entry.Name()) {
			runs++
		}
	}
	finding.Status = "ok"
	finding.Detail = fmt.Sprintf("%d run(s) with captured diagnostics", runs)
	return finding
}

// healthCheckNames is the closed set this verb can report, exported so a
// surface can be tested against the whole list rather than against whichever
// findings one fixture happened to produce.
func healthCheckNames() []string {
	names := []string{"resident-tick"}
	for _, unit := range healthLogUnits {
		names = append(names, "log-"+unit)
	}
	return append(names, "run-logs")
}
