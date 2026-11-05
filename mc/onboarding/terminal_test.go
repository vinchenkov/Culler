package onboarding

import (
	"bytes"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestChooseKeys(t *testing.T) {
	choices := []Choice{{ID: "first", Label: "First"}, {ID: "second", Label: "Second"}, {ID: "third", Label: "Third"}}
	for _, tc := range []struct {
		name, input, want string
		err               error
	}{
		{name: "default", input: "\n", want: "second"},
		{name: "down", input: "\x1b[B\r", want: "third"},
		{name: "up", input: "\x1b[A\r", want: "first"},
		{name: "wrap", input: "\x1b[B\x1b[B\n", want: "first"},
		{name: "number", input: "3\n", want: "third"},
		{name: "invalid number", input: "9\n1\n", want: "first"},
		{name: "back", input: "b", err: ErrBack},
		{name: "left", input: "\x1b[D", err: ErrBack},
		{name: "exit", input: "q", err: ErrExit},
		{name: "interrupt", input: "\x03", err: ErrExit},
		{name: "closed input", input: "", err: ErrExit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			terminal := NewTerminal(strings.NewReader(tc.input), &out)
			got, err := terminal.Choose("Choose agent access", choices, 1)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("choice = %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
			for _, text := range []string{"› 2. Second", "Enter select", "b back", "q exit"} {
				if !strings.Contains(out.String(), text) {
					t.Fatalf("the prompt lacks %q: %s", text, out.String())
				}
			}
		})
	}
}

func TestInputEditsAndConcealsValues(t *testing.T) {
	for _, tc := range []struct {
		name, input, initial, want, shown string
		secret                            bool
		err                               error
	}{
		{name: "default", input: "\n", initial: "/project", want: "/project"},
		{name: "edit", input: "projet\x7fct\n", want: "project"},
		{name: "cursor", input: "prject\x1b[D\x1b[D\x1b[D\x1b[Do\n", want: "project"},
		{name: "unicode", input: "/项目/été\n", want: "/项目/été"},
		{name: "clear", input: "old\x15new\n", want: "new"},
		{name: "secret", input: "fixture-secret\n", initial: "previous-secret", want: "fixture-secret", shown: "Concealed", secret: true},
		{name: "secret default", input: "\n", initial: "previous-secret", want: "previous-secret", shown: "Concealed", secret: true},
		{name: "secret empty", input: "\n", shown: "None", secret: true},
		{name: "back", input: ":back\n", err: ErrBack},
		{name: "exit", input: ":exit\n", err: ErrExit},
		{name: "interrupt", input: "partial\x03", err: ErrExit},
		{name: "closed", input: "partial", err: ErrExit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			terminal := NewTerminal(strings.NewReader(tc.input), &out)
			got, err := terminal.Input("Project value", tc.initial, tc.secret)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("input = %q, %v; want %q, %v", got, err, tc.want, tc.err)
			}
			for _, secret := range []string{"fixture-secret", "previous-secret"} {
				if strings.Contains(out.String(), secret) {
					t.Fatal("the prompt exposed a secret")
				}
			}
			if !strings.Contains(out.String(), "ctrl+b back") || !strings.Contains(out.String(), "ctrl+c exit") {
				t.Fatalf("the prompt lacks its controls: %s", out.String())
			}
			if tc.shown != "" && !strings.Contains(out.String(), "Project value: "+tc.shown) {
				t.Fatalf("the answer does not show %q: %s", tc.shown, out.String())
			}
		})
	}
}

func TestAskShowsProgressAndHintsBeforeTheAnswer(t *testing.T) {
	for _, ansi := range []bool{false, true} {
		var out bytes.Buffer
		terminal := NewTerminal(strings.NewReader("endpoint\n"), &out)
		terminal.columns, terminal.ansi = 40, ansi
		hint := "Use lowercase letters, digits, periods, underscores, or hyphens."
		answer, err := terminal.Ask(Prompt{Label: "Connection name", Progress: "1 of 4", Hints: []string{hint}})
		if answer != "endpoint" || err != nil {
			t.Fatalf("answer = %q, %v", answer, err)
		}
		output := out.String()
		heading, answered := strings.Index(output, "Connection name (1 of 4)"), strings.Index(output, "› Connection name: endpoint")
		if heading < 0 || answered < heading || !strings.Contains(strings.Join(strings.Fields(output), " "), hint) {
			t.Fatalf("the prompt lacks its progress, its hint, or its answer: %q", output)
		}
		if strings.Contains(output[answered:], "1 of 4") || strings.Contains(output[answered:], "lowercase") {
			t.Fatalf("the answer retained the progress or the hint: %q", output[answered:])
		}
		for _, line := range strings.FieldsFunc(regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`).ReplaceAllString(output, "\n"), func(r rune) bool { return r == '\n' || r == '\r' }) {
			if displayWidth(line) > 40 {
				t.Fatalf("the prompt exceeds the terminal width: %q", line)
			}
		}
	}
}

func TestTerminalWidthAndPlainText(t *testing.T) {
	path := "/Users/operator/projects/a-directory-with-a-long-name/项目/vision.md"
	link := "https://docs.example.test/desktop/setup/install/mac-install/"
	for _, columns := range []int{80, 40, 20, 7} {
		var out bytes.Buffer
		terminal := NewTerminal(strings.NewReader("q"), &out)
		terminal.columns = columns
		terminal.Stage(4, "Choose your project", []string{"Prepare this Mac", "Connect an agent"})
		terminal.Panel("Selected workspace", []string{path, link, "The path remains readable."})
		_, _ = terminal.Choose("Confirm the workspace", []Choice{{ID: "yes", Label: "Use this workspace (Recommended)"}}, 0)
		lines := strings.Split(out.String(), "\n")
		for _, line := range lines {
			if displayWidth(line) > columns && line != path && line != link {
				t.Fatalf("width %d exceeded by %q", columns, line)
			}
		}
		// A path or an address stays on one line. The terminal wraps a value that is wider than the terminal.
		for _, value := range []string{path, link} {
			want := value
			if displayWidth(value)+2 <= columns {
				want = "  " + value
			}
			if !slices.Contains(lines, want) {
				t.Fatalf("width %d changed or indented the value %q: %q", columns, value, lines)
			}
		}
		if strings.Contains(out.String(), "\x1b") {
			t.Fatal("a plain terminal received display controls")
		}
	}
}

func TestTerminalRejectsControlText(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(strings.NewReader(""), &out)
	terminal.Panel("Project\x1b[31m", []string{"/project/\x00name", "a\tb"})
	if strings.ContainsAny(out.String(), "\x1b\x00\t") {
		t.Fatalf("the panel retained a terminal control: %q", out.String())
	}
}

func TestActivityReportsActualResult(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		err        error
	}{
		{name: "complete", want: "Complete: Check Docker Desktop"},
		{name: "failed", want: "Failed: Check Docker Desktop", err: errors.New("private diagnostic detail")},
		{name: "interrupted", want: "Stopped: Check Docker Desktop", err: ErrExit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			terminal := NewTerminal(strings.NewReader(""), &out)
			called := 0
			err := terminal.Activity("Check Docker Desktop", func() error {
				called++
				if !strings.Contains(out.String(), "Active: Check Docker Desktop") {
					t.Fatal("the operation started without a status")
				}
				if strings.Contains(out.String(), "Complete:") {
					t.Fatal("the status declared completion before the operation returned")
				}
				return tc.err
			})
			if !errors.Is(err, tc.err) || called != 1 || !strings.Contains(out.String(), tc.want) {
				t.Fatalf("operation = %v, calls = %d, output = %q", err, called, out.String())
			}
			if strings.Contains(out.String(), "%") || strings.Contains(out.String(), "private diagnostic detail") {
				t.Fatalf("the status invented progress or exposed details: %q", out.String())
			}
		})
	}
}

func TestTerminalPreservesReaderFailures(t *testing.T) {
	failure := errors.New("input disconnected")
	terminal := NewTerminal(errorReader{failure}, io.Discard)
	if _, err := terminal.Input("Value", "", true); !errors.Is(err, failure) {
		t.Fatalf("input failure = %v", err)
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }
