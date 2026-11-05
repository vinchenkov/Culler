package onboarding

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// NewCommandTerminal shares setup styles without interactive input controls.
func NewCommandTerminal(out io.Writer, noColor bool) *Terminal {
	t := NewTerminal(nil, out)
	term := os.Getenv("TERM")
	t.ansi = t.output != nil && term != "" && term != "dumb"
	_, disabled := os.LookupEnv("NO_COLOR")
	t.color = t.ansi && !disabled && !noColor
	return t
}

func (t *Terminal) CommandTitle() {
	t.writeLines([]terminalLine{{"Culler", textHeading}, {}})
}

func (t *Terminal) HasTerminal() bool { return t.input != nil && t.output != nil }

func (t *Terminal) Guidance(lines ...string) {
	if !t.afterBlank() {
		t.writeLines([]terminalLine{{}})
	}
	for _, line := range lines {
		t.writeLines(t.lines(line, textMuted, 0))
	}
	t.writeLines([]terminalLine{{}})
}

// Literal preserves copyable commands, paths, and release identities.
func (t *Terminal) Literal(value string) {
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	t.writeLines([]terminalLine{{value, ""}})
}

func (t *Terminal) Field(label, value string) {
	if 19+displayWidth(value) <= t.Width() && displayWidth(label) <= 16 {
		t.writeLines([]terminalLine{{fmt.Sprintf("  %-16s %s", label, value), ""}})
		return
	}
	t.writeLines(t.lines(label, textMuted, 2))
	t.Literal(value)
}

func (t *Terminal) CommandResult(label string, good bool) {
	style := textAction
	if good {
		style = textDone
	}
	t.writeLines(t.lines(label, style, 0))
	t.writeLines([]terminalLine{{}})
}

func (t *Terminal) CommandItem(command, description string) {
	if t.Width() >= 80 && 28+displayWidth(description) <= t.Width() {
		line := "  " + t.styled(command, textCurrent) + strings.Repeat(" ", max(1, 26-displayWidth(command))) + description
		t.writeLines([]terminalLine{{line, ""}})
		return
	}
	t.writeLines(t.lines(command, textCurrent, 2))
	t.writeLines(t.hangingLines(description, "", 4, 0))
	t.writeLines([]terminalLine{{}})
}
