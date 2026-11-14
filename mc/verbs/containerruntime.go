package verbs

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The doctor container-runtime probe: acceptance is by
// capability, never runtime identity — no runtime name or version string is
// ever inspected. Production darwin mc self-delegates every invocation into
// the warm helper, so a doctor that runs at all has already proven the helper
// exec round-trip; the remaining legs are kernel-backed facts probed from
// inside the container. This file holds the OS-independent parsers; the
// setuid legs live in the linux build.

var uidMapIdentityLine = regexp.MustCompile(`^\s*0\s+0\s+4294967295\s*$`)

// uidMapIsIdentity reports whether /proc/self/uid_map is the single identity
// line — the no-uid-remap / no-Enhanced-Container-Isolation canary.
func uidMapIsIdentity(content string) bool {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	return len(lines) == 1 && uidMapIdentityLine.MatchString(lines[0])
}

// noNewPrivsFlag extracts NoNewPrivs from /proc/self/status. A missing line
// is an error, never an assumed zero: the setuid gate is dead with the flag
// forced, so the probe must fail closed on an unreadable answer.
func noNewPrivsFlag(status string) (int, error) {
	for _, line := range strings.Split(status, "\n") {
		value, found := strings.CutPrefix(line, "NoNewPrivs:")
		if !found {
			continue
		}
		flag, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("unparseable NoNewPrivs line %q", line)
		}
		return flag, nil
	}
	return 0, fmt.Errorf("no NoNewPrivs line in process status")
}
