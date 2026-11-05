package onboarding

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"
)

const progressRefresh = time.Second

type terminalOutput struct {
	mu       sync.Mutex
	writer   io.Writer
	sequence uint64
}

func (out *terminalOutput) Write(data []byte) (int, error) {
	out.mu.Lock()
	defer out.mu.Unlock()
	return out.write(data)
}

func (out *terminalOutput) write(data []byte) (int, error) {
	out.sequence++
	n, err := out.writer.Write(data)
	if flush, ok := out.writer.(interface{ Flush() error }); ok && err == nil {
		err = flush.Flush()
	}
	return n, err
}

// ActivityProgress displays operation reports. Concurrent reports are safe.
// Repeated reports and reports after the action returns have no effect.
func (t *Terminal) ActivityProgress(label string, action func(report func(string)) error) error {
	ticker := time.NewTicker(progressRefresh)
	defer ticker.Stop()
	return t.activityProgress(label, action, time.Now, ticker.C)
}

func (t *Terminal) activityProgress(label string, action func(func(string)) error, now func() time.Time, ticks <-chan time.Time) (retErr error) {
	if action == nil {
		return fmt.Errorf("the setup operation is absent")
	}
	label = progressText(label)
	state := activityProgress{
		terminal: t,
		now:      now,
		printed:  now(),
		frame:    terminalFrame{terminal: t},
	}
	state.label = label
	if t.ansi {
		state.frame.paint(t.lines("› "+label+" .", textCurrent, 0), false, 0)
	} else {
		t.progressLines("Active: " + label)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-ticks:
				state.refresh()
			}
		}
	}()
	returned := false
	defer func() {
		state.mu.Lock()
		state.closed = true
		state.mu.Unlock()
		close(stop)
		<-done
		state.render(now(), true)
		result := retErr
		if !returned {
			result = ErrExit
		}
		if t.ansi {
			state.frame.paint(t.resultLines(label, result), false, 0)
		} else {
			t.activityResult(label, result)
		}
	}()
	retErr = action(state.report)
	returned = true
	return retErr
}

type activityProgress struct {
	mu       sync.Mutex
	terminal *Terminal
	now      func() time.Time
	printed  time.Time
	current  string
	display  string
	closed   bool
	frame    terminalFrame
	label    string
	phase    int
}

func (state *activityProgress) report(message string) {
	message = progressText(message)
	if message == "" {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.closed {
		return
	}
	state.current = message
	state.render(state.now(), false)
}

func (state *activityProgress) refresh() {
	state.mu.Lock()
	defer state.mu.Unlock()
	if !state.closed {
		state.render(state.now(), false)
	}
}

func (state *activityProgress) render(now time.Time, finish bool) {
	if state.current == state.display && (!state.terminal.ansi || finish) {
		return
	}
	if !finish && state.display != "" && now.Sub(state.printed) < progressRefresh {
		return
	}
	if state.terminal.ansi {
		message := state.current
		if message == "" {
			message = state.label
		}
		state.phase = (state.phase + 1) % 3
		lines := state.terminal.lines("› "+message+" "+strings.Repeat(".", state.phase+1), textCurrent, 0)
		state.frame.paint(lines, false, 0)
	} else {
		state.terminal.progressLines("Update: " + state.current)
	}
	state.display = state.current
	state.printed = now
}

func (t *Terminal) progressLines(lines ...string) {
	var text strings.Builder
	for _, line := range lines {
		if t.Width() == 1 {
			line = strings.Map(func(r rune) rune {
				if runeWidth(r) > 1 {
					return '?'
				}
				return r
			}, line)
		}
		for _, part := range t.wrap(line) {
			text.WriteString(part)
			text.WriteByte('\n')
		}
	}
	fmt.Fprint(t.out, text.String())
}

func progressText(text string) string {
	const maxRunes = 512
	if len(text) > 16384 {
		text = text[:16384]
	}
	var result strings.Builder
	state, count := 0, 0
	for _, r := range text {
		switch state {
		case 1: // Escape sequence.
			switch r {
			case '[':
				state = 2
			case ']', 'P', 'X', '^', '_':
				state = 3
			default:
				if r >= 0x30 && r <= 0x7e {
					state = 0
				}
			}
			continue
		case 2: // Control sequence.
			if r >= 0x40 && r <= 0x7e {
				state = 0
			}
			continue
		case 3: // Control string.
			if r == '\a' || r == '\u009c' {
				state = 0
			} else if r == '\x1b' {
				state = 4
			}
			continue
		case 4:
			state = 3
			if r == '\\' || r == '\a' || r == '\u009c' {
				state = 0
			} else if r == '\x1b' {
				state = 4
			}
			continue
		}
		switch r {
		case '\x1b':
			state = 1
			continue
		case '\u009b':
			state = 2
			continue
		case '\u0090', '\u0098', '\u009d', '\u009e', '\u009f':
			state = 3
			continue
		}
		if unicode.IsSpace(r) {
			r = ' '
		} else if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		if count == maxRunes {
			result.WriteString("...")
			break
		}
		result.WriteRune(r)
		count++
	}
	return strings.Join(strings.Fields(result.String()), " ")
}
