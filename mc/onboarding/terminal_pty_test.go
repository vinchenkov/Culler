//go:build darwin || linux

package onboarding

import (
	"errors"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func readTerminalUntil(t *testing.T, master *os.File, text string) string {
	t.Helper()
	var output strings.Builder
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		fds := []unix.PollFd{{Fd: int32(master.Fd()), Events: unix.POLLIN}}
		count, err := unix.Poll(fds, 50)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			continue
		}
		buffer := make([]byte, 4096)
		size, err := master.Read(buffer)
		if err != nil {
			t.Fatal(err)
		}
		output.Write(buffer[:size])
		if strings.Contains(output.String(), text) {
			return output.String()
		}
	}
	t.Fatalf("the terminal did not display %q: %q", text, output.String())
	return ""
}

func TestRealTerminalArrowKeysRestoreState(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	for _, tc := range []struct {
		name, input, want string
		err               error
	}{
		{name: "choice", input: "\x1b[B\r", want: "second"},
		{name: "back", input: "b", err: ErrBack},
		{name: "interrupt", input: "\x03", err: ErrExit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, slave := testTerminalPair(t)
			original, err := getTerminalState(int(slave.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: 40, Row: 24}); err != nil {
				t.Fatal(err)
			}
			terminal := NewTerminal(slave, slave)
			if terminal.Width() != 40 || terminal.color {
				t.Fatalf("terminal width = %d, color = %v", terminal.Width(), terminal.color)
			}
			type result struct {
				value string
				err   error
			}
			done := make(chan result, 1)
			go func() {
				value, err := terminal.Choose("Choose agent access", []Choice{
					{ID: "first", Label: "First (Recommended)"}, {ID: "second", Label: "Second"},
				}, 0)
				done <- result{value, err}
			}()
			output := readTerminalUntil(t, master, "q exit")
			if strings.Contains(output, "\x1b[36m") {
				t.Fatal("NO_COLOR retained a color sequence")
			}
			if _, err := master.Write([]byte(tc.input)); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-done:
				if got.value != tc.want || !errors.Is(got.err, tc.err) {
					t.Fatalf("terminal result = %#v; want %q, %v", got, tc.want, tc.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the terminal did not accept the key")
			}
			restored, err := getTerminalState(int(slave.Fd()))
			if err != nil || !reflect.DeepEqual(restored, original) {
				t.Fatalf("the terminal state did not return: %v, %v", restored, err)
			}
		})
	}
}

func TestRealTerminalConcealsSecret(t *testing.T) {
	t.Setenv("TERM", "xterm")
	master, slave := testTerminalPair(t)
	terminal := NewTerminal(slave, slave)
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := terminal.Input("Agent credential", "previous-secret", true)
		done <- result{value, err}
	}()
	output := readTerminalUntil(t, master, "> ")
	if _, err := master.Write([]byte("\x15fixture-secret\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.value != "fixture-secret" || got.err != nil {
			t.Fatalf("the terminal returned an incorrect secret: %v", got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the terminal did not accept the secret")
	}
	output += readTerminalUntil(t, master, "*")
	if strings.Contains(output, "fixture-secret") || strings.Contains(output, "previous-secret") {
		t.Fatal("the terminal exposed a secret")
	}
}

func TestRealTerminalRestoresStateAfterSignal(t *testing.T) {
	_, slave := testTerminalPair(t)
	original, err := getTerminalState(int(slave.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	terminal := NewTerminal(slave, slave)
	keys, err := terminal.openKeys()
	if err != nil {
		t.Fatal(err)
	}
	signals := make(chan os.Signal, 1)
	keys.signals = signals
	signals <- os.Interrupt
	if _, err := keys.read(); !errors.Is(err, ErrExit) {
		t.Fatalf("the signal returned %v", err)
	}
	if err := keys.close(); err != nil {
		t.Fatal(err)
	}
	restored, err := getTerminalState(int(slave.Fd()))
	if err != nil || !reflect.DeepEqual(restored, original) {
		t.Fatalf("the signal left terminal controls active: %v, %v", restored, err)
	}
}

func TestRealTerminalChoiceResizesAndCollapsesToItsAnswer(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("NO_COLOR", "1")
	master, slave := testTerminalPair(t)
	resize := func(columns uint16) {
		t.Helper()
		if err := unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Col: columns, Row: 24}); err != nil {
			t.Fatal(err)
		}
	}
	resize(80)
	terminal := NewTerminal(slave, slave)
	done := make(chan error, 1)
	go func() {
		_, err := terminal.Choose("Choose an account", []Choice{
			{ID: "first", Label: "Use credentials from the protected system account store", Detail: "The source credential stays intact."},
			{ID: "second", Label: "Connect another account", Detail: "A browser opens for the new account."},
		}, 0)
		done <- err
	}()
	readTerminalUntil(t, master, "q exit")
	resize(40)
	resized := readTerminalUntil(t, master, "q exit")
	controls := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	for _, line := range strings.Split(controls.ReplaceAllString(resized, ""), "\n") {
		if displayWidth(strings.Trim(line, "\r")) > 40 {
			t.Fatalf("the resized prompt exceeds forty columns: %q", line)
		}
	}
	if !strings.Contains(resized, "\x1b[J") {
		t.Fatal("resize did not replace the earlier prompt")
	}
	if _, err := master.Write([]byte("\x1b[B")); err != nil {
		t.Fatal(err)
	}
	selected := readTerminalUntil(t, master, "q exit")
	if strings.Contains(selected, "source credential") || !strings.Contains(selected, "browser opens") {
		t.Fatal("the selected detail did not follow the arrow key")
	}
	if _, err := master.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	answer := readTerminalUntil(t, master, "Connect another account")
	if strings.Contains(answer, "Choose an account") || strings.Contains(answer, "q exit") || strings.Contains(answer, "✓") || !strings.Contains(answer, "\x1b[J") {
		t.Fatalf("the completed choice did not collapse to a neutral answer: %q", answer)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the terminal did not return after the answer")
	}
}
