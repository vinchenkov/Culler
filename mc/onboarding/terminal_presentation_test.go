package onboarding

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

func TestStageDistinguishesPendingAndCompletedWork(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(nil, &out)
	terminal.Stage(4, "Review your setup", []string{"welcome", "prepare"})
	text := out.String()
	if strings.Count(text, "✓") != 2 || !strings.Contains(text, "✓  ✓  ○  ●  ·  ·") {
		t.Fatalf("the stage display confuses pending and completed work: %q", text)
	}
	if !strings.Contains(text, "4 of 6") || !strings.Contains(text, "Pending: Connect an agent") || !strings.Contains(text, "Next: Start Culler") {
		t.Fatalf("the stage display omits its position: %q", text)
	}
	terminal.Stage(6, "Open your dashboard", stageIDs[:5])
	if strings.Contains(out.String(), "Mac setup complete") {
		t.Fatal("the final stage declared completion before the final save")
	}
	before := out.Len()
	terminal.Complete()
	completion := out.String()[before:]
	if strings.Count(completion, "✓") != 6 || strings.ContainsAny(completion, "●○·") || !strings.Contains(completion, "Mac setup complete") {
		t.Fatalf("the final state remains incomplete: %q", completion)
	}
	if len(regexp.MustCompile(`(?m)^Culler$`).FindAllString(out.String(), -1)) != 1 || strings.Contains(out.String(), "+---") {
		t.Fatal("the stage display repeats its identity or box borders")
	}
}

func TestChoiceDetailsFollowSelectionAndWrapUnderTheLabel(t *testing.T) {
	terminal := NewTerminal(nil, nil)
	terminal.columns, terminal.ansi = 40, true
	choices := []Choice{
		{ID: "first", Label: "Use credentials from the protected system account store", Detail: "The selected account retains its source credential."},
		{ID: "second", Label: "Connect another account", Detail: "A browser opens for the new account."},
	}
	for selected := range choices {
		lines := terminal.choiceLines("Choose an account", choices, selected)
		var text strings.Builder
		for index, line := range lines {
			if displayWidth(line.text) > 40 {
				t.Fatalf("the choice exceeds the terminal width: %q", line.text)
			}
			if line.style == textCurrent && index > 1 && !strings.HasPrefix(line.text, "› ") && !strings.HasPrefix(line.text, "  ") {
				t.Fatalf("the choice continuation lost its indentation: %q", line.text)
			}
			text.WriteString(strings.TrimSpace(line.text))
		}
		other := choices[1-selected].Detail
		if strings.Contains(text.String(), other) {
			t.Fatal("the display includes an unselected choice's detail")
		}
		if !strings.Contains(strings.ReplaceAll(text.String(), " ", ""), strings.ReplaceAll(choices[selected].Detail, " ", "")) {
			t.Fatal("the display omitted the selected choice's detail")
		}
	}
}

func TestActionRequiredCannotReportCompletion(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		var out bytes.Buffer
		terminal := NewTerminal(nil, &out)
		terminal.ansi, terminal.color = interactive, interactive
		err := terminal.Activity("Docker Desktop", func() error { return ErrActionRequired })
		if !errors.Is(err, ErrActionRequired) || strings.Contains(out.String(), "✓") || strings.Contains(out.String(), "Complete") {
			t.Fatalf("the prerequisite reports completion: %v, %q", err, out.String())
		}
		if !strings.Contains(out.String(), "Docker Desktop requires attention") || strings.Contains(out.String(), "Failed:") {
			t.Fatalf("the prerequisite looks like an operation failure: %q", out.String())
		}
		if strings.Contains(out.String(), "duration") || strings.Contains(out.String(), "Elapsed") {
			t.Fatal("the display includes time text")
		}
	}
}

func TestPromptsKeepTheStepCountInTheStageHeading(t *testing.T) {
	controls := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	for _, columns := range []int{40, 80} {
		for _, mode := range []string{"plain", "color", "no-color"} {
			t.Run(fmt.Sprintf("%d/%s", columns, mode), func(t *testing.T) {
				var out bytes.Buffer
				terminal := NewTerminal(strings.NewReader("\n\n\n"), &out)
				terminal.columns, terminal.ansi, terminal.color = columns, mode != "plain", mode == "color"
				terminal.Stage(4, "Review your setup", stageIDs[:3])
				if _, err := terminal.Input("Project folder path", "", false); err != nil {
					t.Fatal(err)
				}
				for range 25 {
					terminal.Panel("Project details", []string{"Your project uses a Git repository."})
				}
				if _, err := terminal.Choose("Allow this agent access?", []Choice{{ID: "accept", Label: "Allow this access"}}, 0); err != nil {
					t.Fatal(err)
				}
				terminal.Stage(5, "Review your setup", stageIDs[:4])
				if _, err := terminal.Choose("Start with these choices?", []Choice{{ID: "start", Label: "Start Culler"}}, 0); err != nil {
					t.Fatal(err)
				}
				output := controls.ReplaceAllString(out.String(), "")
				for _, count := range []string{"4 of 6", "5 of 6"} {
					if strings.Count(output, count) != 1 {
						t.Fatalf("the step count must appear once: %q", output)
					}
				}
				for _, prompt := range []string{"Project folder path", "Allow this agent access?", "Start with these choices?"} {
					if !strings.Contains(output, prompt) {
						t.Fatalf("the display omits the current task %q: %q", prompt, output)
					}
				}
				for _, line := range strings.Split(output, "\n") {
					if displayWidth(strings.Trim(line, "\r")) > columns {
						t.Fatalf("the prompt exceeds the terminal width: %q", line)
					}
				}
			})
		}
	}
}

func TestStageDestinationOverrideDoesNotChangeTheNormalSequence(t *testing.T) {
	var out bytes.Buffer
	terminal := NewTerminal(nil, &out)
	terminal.StageWithNext(3, "Connect an agent", stageIDs[:2], "Start Culler")
	if !strings.Contains(out.String(), "Next: Start Culler") {
		t.Fatal("the destination override is absent")
	}
	out.Reset()
	terminal.Stage(3, "Connect an agent", stageIDs[:2])
	if !strings.Contains(out.String(), "Next: Review your setup") || strings.Contains(out.String(), "Next: Start Culler") {
		t.Fatal("the override changed the normal sequence")
	}
}
