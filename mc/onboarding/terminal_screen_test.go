package onboarding

import (
	"bytes"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

var testScreenControl = regexp.MustCompile(`^\x1b\[([0-9;]*)([A-Za-z])`)

// testScreen replays the cursor controls of the terminal output on a screen with a fixed size.
func testScreen(output string, columns, rows int) []string {
	cells := make([][]rune, rows)
	blank := func() []rune { return []rune(strings.Repeat(" ", columns)) }
	for y := range cells {
		cells[y] = blank()
	}
	x, y := 0, 0
	advance := func() {
		if y++; y == rows {
			cells = append(cells[1:], blank())
			y--
		}
	}
	for output != "" {
		if match := testScreenControl.FindStringSubmatch(output); match != nil {
			output = output[len(match[0]):]
			n, _ := strconv.Atoi(strings.Split(match[1], ";")[0])
			switch match[2] {
			case "A":
				y = max(0, y-max(n, 1))
			case "D":
				x = max(0, x-max(n, 1))
			case "J":
				copy(cells[y][min(x, columns):], blank())
				for row := y + 1; row < rows; row++ {
					cells[row] = blank()
				}
			case "K":
				copy(cells[y], blank())
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(output)
		output = output[size:]
		switch {
		case r == '\r':
			x = 0
		case r == '\n':
			x = 0
			advance()
		case r >= ' ':
			if x >= columns {
				x = 0
				advance()
			}
			cells[y][x] = r
			x++
		}
	}
	lines := []string{}
	for _, row := range cells {
		lines = append(lines, strings.TrimRight(string(row), " "))
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func TestAskShowsCorrectionsOnlyUntilAnAcceptedAnswer(t *testing.T) {
	for _, color := range []bool{false, true} {
		var out bytes.Buffer
		terminal := NewTerminal(strings.NewReader("\nMy Endpoint\nendpoint\n"), &out)
		terminal.columns, terminal.ansi, terminal.color = 40, true, color
		checked := []string{}
		answer, err := terminal.Ask(Prompt{Label: "Connection name", Check: func(name string) *Correction {
			checked = append(checked, name)
			switch name {
			case "":
				return &Correction{Title: "Enter a connection name", Lines: []string{"The connection name is empty."}}
			case "My Endpoint":
				return &Correction{Title: "Check the connection name", Lines: []string{"You entered: My Endpoint", "Replace each space with a hyphen."}}
			}
			return nil
		}})
		if answer != "endpoint" || err != nil || !slices.Equal(checked, []string{"", "My Endpoint", "endpoint"}) {
			t.Fatalf("answer = %q, %v; checked = %q", answer, err, checked)
		}
		mark := "! Check the connection name"
		if color {
			mark = "\x1b[33m" + mark + "\x1b[0m"
		}
		if !strings.Contains(out.String(), mark) || !strings.Contains(out.String(), "You entered: My Endpoint") {
			t.Fatalf("the correction lacks its problem mark or the rejected value: %q", out.String())
		}
		if strings.Contains(out.String(), "› Connection name: My Endpoint") || strings.Contains(out.String(), "None") {
			t.Fatalf("a rejected answer appeared as an answer: %q", out.String())
		}
		if screen := testScreen(out.String(), 40, 24); !slices.Equal(screen, []string{"› Connection name: endpoint"}) {
			t.Fatalf("the accepted answer did not replace the correction: %q", screen)
		}
	}
}

func TestAskShowsAnEarlierCorrectionWithTheFirstRequest(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(strings.NewReader("second-endpoint\n"), &out)
	terminal.columns, terminal.ansi = 40, true
	correction := &Correction{Title: "Choose another connection name", Lines: []string{"Culler already uses this name."}}
	if _, err := terminal.Ask(Prompt{Label: "Connection name", Correction: correction}); err != nil {
		t.Fatal(err)
	}
	before, _, _ := strings.Cut(out.String(), "Connection name\n")
	if !strings.Contains(before, "! Choose another connection name") {
		t.Fatalf("the earlier correction is absent from the prompt: %q", out.String())
	}
	if screen := testScreen(out.String(), 40, 24); !slices.Equal(screen, []string{"› Connection name: second-endpoint"}) {
		t.Fatalf("the accepted answer did not replace the correction: %q", screen)
	}
}

func TestConsecutiveAnswersShareNoBlankLine(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(strings.NewReader("endpoint\nendpoint-model\n\r"), &out)
	terminal.columns, terminal.ansi = 40, true
	terminal.Panel("Connect an Anthropic-compatible endpoint", nil)
	for _, label := range []string{"Connection name", "Model identifier"} {
		if _, err := terminal.Input(label, "", false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := terminal.Choose("Test this connection?", []Choice{{ID: "check", Label: "Test endpoint"}}, 0); err != nil {
		t.Fatal(err)
	}
	terminal.Panel("Your agent", []string{"Claude Code (endpoint)"})
	want := []string{
		"Connect an Anthropic-compatible endpoint",
		"",
		"› Connection name: endpoint",
		"› Model identifier: endpoint-model",
		"› Test endpoint",
		"",
		"Your agent",
		"  Claude Code (endpoint)",
	}
	if screen := testScreen(out.String(), 40, 24); !slices.Equal(screen, want) {
		t.Fatalf("screen = %q", screen)
	}
}

func TestEachBlockHasOneBlankLineAbove(t *testing.T) {
	for _, ansi := range []bool{false, true} {
		var out bytes.Buffer
		terminal := NewTerminal(nil, &out)
		terminal.columns, terminal.ansi, terminal.identity = 40, ansi, true
		result := func(label string, err error) []string {
			if !ansi {
				return []string{"Active: " + label, strings.TrimSpace(string(terminal.resultLines(label, err)[0].text))}
			}
			return []string{terminal.resultLines(label, err)[0].text}
		}
		stage := []string{"✓  ✓  ✓  ●  ·  ·", "4 of 6  Review your setup", "Next: Start Culler"}
		failure := errors.New("fixture failure")
		_ = terminal.Activity("Test this connection", func() error { return nil })
		terminal.Panel("Your agent", []string{"Claude Code (endpoint)"})
		terminal.Stage(4, "Review your setup", stageIDs[:3])
		_ = terminal.Activity("Test Codex connection", func() error { return nil })
		terminal.Stage(4, "Review your setup", stageIDs[:3])
		_ = terminal.Activity("Test Codex connection", func() error { return failure })
		terminal.Problem("", []string{"The cause is unknown."})
		terminal.Stage(4, "Review your setup", stageIDs[:3])
		want := result("Test this connection", nil)
		want = append(want, "", "Your agent", "  Claude Code (endpoint)", "")
		want = append(want, stage...)
		want = append(want, "")
		want = append(want, result("Test Codex connection", nil)...)
		want = append(want, "")
		want = append(want, stage...)
		want = append(want, "")
		want = append(want, result("Test Codex connection", failure)...)
		want = append(want, "  The cause is unknown.", "")
		want = append(want, stage...)
		if screen := testScreen(out.String(), 40, 40); !slices.Equal(screen, want) {
			t.Fatalf("ansi %v: screen = %q\nwant %q", ansi, screen, want)
		}
	}
}

func TestChoiceDescriptionIndentsPastTheLabelText(t *testing.T) {
	choices := []Choice{
		{ID: "token", Label: "Enter the provider token again"},
		{ID: "values", Label: "Change the connection values", Detail: "Each prompt except the provider token shows its saved value."},
		{ID: "other", Label: "Choose another way to connect"},
	}
	for _, tc := range []struct {
		ansi bool
		want []string
	}{
		{true, []string{
			"Choose how to continue",
			"  Enter the provider token again",
			"› Change the connection values",
			"    Each prompt except the provider",
			"    token shows its saved value.",
			"  Choose another way to connect",
		}},
		{false, []string{
			"Choose how to continue",
			"  1. Enter the provider token again",
			"› 2. Change the connection values",
			"       Each prompt except the provider",
			"       token shows its saved value.",
			"  3. Choose another way to connect",
		}},
	} {
		terminal := NewTerminal(nil, nil)
		terminal.columns, terminal.ansi = 40, tc.ansi
		var text []string
		for _, line := range terminal.choiceLines("Choose how to continue", choices, 1) {
			text = append(text, line.text)
		}
		if len(text) < len(tc.want) || !slices.Equal(text[:len(tc.want)], tc.want) {
			t.Fatalf("ansi %v: choices = %q", tc.ansi, text)
		}
	}
}

func TestChooseShowsAnUnavailableNumberOnlyUntilAValidChoice(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(strings.NewReader("9\r\r"), &out)
	terminal.columns, terminal.ansi = 40, true
	choice, err := terminal.Choose("Choose how to connect", []Choice{{ID: "first", Label: "Sign in to Codex"}}, 0)
	if choice != "first" || err != nil {
		t.Fatalf("choice = %q, %v", choice, err)
	}
	if !strings.Contains(out.String(), "! Choose an available option") {
		t.Fatalf("the correction lacks its problem mark: %q", out.String())
	}
	if screen := testScreen(out.String(), 40, 24); !slices.Equal(screen, []string{"› Sign in to Codex"}) {
		t.Fatalf("screen = %q", screen)
	}
}

func TestProblemMarksOnlyAnExplicitProblem(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(nil, &out)
	terminal.columns = 40
	terminal.Panel("The previous connection failed", []string{"Setup kept the saved values."})
	terminal.Problem("Claude Code (second-endpoint) connection failed", []string{"The cause is unknown."})
	want := []string{
		"The previous connection failed",
		"  Setup kept the saved values.",
		"",
		"! Claude Code (second-endpoint)",
		"  connection failed",
		"  The cause is unknown.",
	}
	if screen := testScreen(out.String(), 40, 24); !slices.Equal(screen, want) {
		t.Fatalf("screen = %q", screen)
	}
}

func TestPanelAndHintLinesHangUnderTheirFirstLine(t *testing.T) {
	terminal := NewTerminal(nil, nil)
	terminal.columns = 40
	panel := terminal.panelLines("Resume this connection", []string{"Model identifier: endpoint-model", "Endpoint address: https://endpoint.example/anthropic"})
	if !slices.Equal(panel, []string{
		"Resume this connection",
		"  Model identifier: endpoint-model",
		"  Endpoint address:",
		"    https://endpoint.example/anthropic",
		"",
	}) {
		t.Fatalf("panel = %q", panel)
	}
	lines, _ := terminal.inputLines(Prompt{Label: "Connection name", Progress: "1 of 4", Hints: []string{"Use lowercase letters, digits, periods, underscores, or hyphens."}}, nil, 0)
	var text []string
	for _, line := range lines {
		text = append(text, line.text)
	}
	if !slices.Equal(text[:3], []string{"Connection name (1 of 4)", "  Use lowercase letters, digits,", "    periods, underscores, or hyphens."}) {
		t.Fatalf("prompt = %q", text)
	}
}

func TestPanelKeepsADiagnosticPathOnOneLine(t *testing.T) {
	path := "/tmp/mc-setup-3930209623/diagnostic.json"
	for _, tc := range []struct {
		columns int
		want    string
	}{{80, "  " + path}, {40, path}, {30, path}} {
		terminal := NewTerminal(nil, nil)
		terminal.columns = tc.columns
		lines := terminal.panelLines("", []string{"Share this diagnostic record with your coding harness:", path})
		if !slices.Contains(lines, tc.want) {
			t.Fatalf("width %d: panel = %q; want the line %q", tc.columns, lines, tc.want)
		}
	}
	var out bytes.Buffer
	terminal := NewTerminal(nil, &out)
	terminal.columns = 30
	terminal.Problem("", []string{"The cause is unknown.", path})
	want := []string{"  The cause is unknown.", path[:30], path[30:]}
	if screen := testScreen(out.String(), 30, 24); !slices.Equal(screen, want) {
		t.Fatalf("screen = %q", screen)
	}
}
