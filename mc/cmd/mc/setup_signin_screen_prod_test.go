//go:build !test_fake_routing && (darwin || linux)

package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"mc/onboarding"
)

var setupScreenControl = regexp.MustCompile(`^\x1b\[([0-9;]*)([A-Za-z])`)

// setupScreen replays terminal output on a screen with a fixed size, as a terminal emulator does.
type setupScreen struct {
	columns, rows, x, y int
	cells               [][]rune
}

func newSetupScreen(columns, rows int) *setupScreen {
	screen := &setupScreen{columns: columns, rows: rows}
	for range rows {
		screen.cells = append(screen.cells, screen.blank())
	}
	return screen
}

func (s *setupScreen) blank() []rune { return []rune(strings.Repeat(" ", s.columns)) }

func (s *setupScreen) advance() {
	if s.y++; s.y == s.rows {
		s.cells = append(s.cells[1:], s.blank())
		s.y--
	}
}

func (s *setupScreen) feed(output string) {
	for output != "" {
		if match := setupScreenControl.FindStringSubmatch(output); match != nil {
			output = output[len(match[0]):]
			n, _ := strconv.Atoi(strings.Split(match[1], ";")[0])
			switch match[2] {
			case "A":
				s.y = max(0, s.y-max(n, 1))
			case "D":
				s.x = max(0, s.x-max(n, 1))
			case "J":
				copy(s.cells[s.y][min(s.x, s.columns):], s.blank())
				for row := s.y + 1; row < s.rows; row++ {
					s.cells[row] = s.blank()
				}
			case "K":
				copy(s.cells[s.y], s.blank())
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(output)
		output = output[size:]
		switch {
		case r == '\r':
			s.x = 0
		case r == '\n':
			s.advance()
		case r >= ' ':
			if s.x >= s.columns {
				s.x = 0
				s.advance()
			}
			s.cells[s.y][s.x] = r
			s.x++
		}
	}
}

func (s *setupScreen) lines() []string {
	lines := []string{}
	for _, row := range s.cells {
		lines = append(lines, strings.TrimRight(string(row), " "))
	}
	return lines
}

// setupScreenSession drives setup through a pseudo-terminal with 24 rows.
type setupScreenSession struct {
	t       *testing.T
	master  *os.File
	screen  *setupScreen
	visible strings.Builder
	raw     strings.Builder
	offset  int
}

func (s *setupScreenSession) read(timeout time.Duration) bool {
	fds := []unix.PollFd{{Fd: int32(s.master.Fd()), Events: unix.POLLIN}}
	count, err := unix.Poll(fds, int(timeout.Milliseconds()))
	if errors.Is(err, unix.EINTR) || count == 0 {
		return false
	}
	if err != nil {
		s.t.Fatal(err)
	}
	buffer := make([]byte, 65536)
	size, err := s.master.Read(buffer)
	if err != nil {
		s.t.Fatal(err)
	}
	output := string(buffer[:size])
	s.raw.WriteString(output)
	s.screen.feed(output)
	s.visible.WriteString(regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`).ReplaceAllString(output, ""))
	return true
}

// wait returns the screen after the output shows text and then stays unchanged.
func (s *setupScreenSession) wait(text string) []string {
	s.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(s.visible.String()[s.offset:], text) {
		if time.Now().After(deadline) {
			s.t.Fatalf("the screen did not show %q: %q", text, s.screen.lines())
		}
		s.read(50 * time.Millisecond)
	}
	for s.read(150 * time.Millisecond) {
	}
	s.offset = s.visible.Len()
	return s.screen.lines()
}

func (s *setupScreenSession) send(input string) {
	s.t.Helper()
	if _, err := s.master.Write([]byte(input)); err != nil {
		s.t.Fatal(err)
	}
}

func TestTerminalSetupSignInConfirmationFitsTheScreen(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	for _, columns := range []int{80, 40} {
		for _, color := range []bool{true, false} {
			for _, harness := range []string{"codex", "claude-sdk"} {
				t.Run(fmt.Sprintf("%s-%d-color-%t", harness, columns, color), func(t *testing.T) {
					if color {
						if value, set := os.LookupEnv("NO_COLOR"); set {
							_ = os.Unsetenv("NO_COLOR")
							t.Cleanup(func() { _ = os.Setenv("NO_COLOR", value) })
						}
					} else {
						t.Setenv("NO_COLOR", "1")
					}
					master, slave := setupTerminalPair(t)
					if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(columns), Row: 24}); err != nil {
						t.Fatal(err)
					}
					w, _ := setupTestWizard(t)
					w.deps.identity = func(string, string) (string, error) { return "", nil }
					terminal := onboarding.NewTerminal(slave, slave)
					terminal.SetStageTitles(setupStageTitles)
					w.ui, w.input, w.output = terminal, slave, terminal
					w.deps.connect = func(*setupWizard, setupConnection) error { t.Fatal("The confirmation started a test"); return nil }
					done := make(chan error, 1)
					go func() {
						w.session.Stage, w.session.Completed = 2, []string{"welcome", "prepare"}
						w.ui.Stage(3, setupStageTitles[2], w.session.Completed)
						done <- w.agents()
					}()
					session := &setupScreenSession{t: t, master: master, screen: newSetupScreen(columns, 24)}
					screen := session.wait("q exit")
					menu := strings.Join(screen, "\n")
					if !strings.Contains(menu, "Sign in to Codex") || !strings.Contains(menu, "Sign in to Claude Code") || strings.Contains(menu, "provider token") {
						t.Fatalf("The menu contains unsupported choices:\n%s", menu)
					}
					if harness == "claude-sdk" {
						session.send("\x1b[B\r")
					} else {
						session.send("\r")
					}
					screen = session.wait("Sign in and connect?")
					text := strings.Join(screen, "\n")
					for _, line := range []string{"Culler", "✓  ✓  ●  ·  ·  ·", "3 of 6  Connect an agent", "Sign in and connect?", "↑↓ move  enter select  b back  q exit"} {
						if !slices.Contains(screen, line) {
							t.Fatalf("The confirmation lacks %q:\n%s", line, text)
						}
					}
					session.send("q")
					select {
					case err := <-done:
						if !errors.Is(err, onboarding.ErrExit) {
							t.Fatalf("The confirmation exit = %v", err)
						}
					case <-time.After(10 * time.Second):
						t.Fatal("Setup did not exit")
					}
				})
			}
		}
	}
}
