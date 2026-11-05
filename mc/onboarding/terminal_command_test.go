package onboarding

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestCommandTerminalPreservesLiteralValuesAtFortyColumns(t *testing.T) {
	var output bytes.Buffer
	terminal := NewCommandTerminal(&output, false)
	terminal.columns = 40
	command := "culler --mc-home '/private/tmp/a long deployment name' restart dashboard"
	identity := strings.Repeat("a", 40)
	terminal.Field("Release", identity)
	terminal.Literal(command)
	if !strings.Contains(output.String(), identity+"\n") || !strings.Contains(output.String(), command+"\n") {
		t.Fatal(output.String())
	}
	if strings.Contains(output.String(), "\x1b") {
		t.Fatal("redirected values contain controls")
	}
}

func TestCommandFieldAccountsForItsLabelWidth(t *testing.T) {
	var output bytes.Buffer
	terminal := NewCommandTerminal(&output, false)
	terminal.columns = 40
	terminal.Field("Home", strings.Repeat("a", 25))
	for _, line := range strings.Split(output.String(), "\n") {
		if len(line) > 40 {
			t.Fatal(output.String())
		}
	}
}

func TestControlCCarriesCancellationAndSetupExit(t *testing.T) {
	terminal := NewTerminal(strings.NewReader("\x03"), io.Discard)
	_, err := terminal.Choose("Continue?", []Choice{{ID: "continue", Label: "Continue"}}, 0)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrExit) {
		t.Fatal(err)
	}
}

func TestCommandConfirmationCancelsWithoutAPreviousStep(t *testing.T) {
	for _, input := range []string{"\x02", ":back\n"} {
		var out bytes.Buffer
		terminal := NewCommandInput(strings.NewReader(input), &out, true)
		_, err := terminal.Ask(Prompt{Label: "Confirm removal", CancelOnly: true})
		if !errors.Is(err, ErrExit) || errors.Is(err, ErrBack) {
			t.Fatalf("The confirmation selected a previous step: %v", err)
		}
		if !strings.Contains(out.String(), "ctrl+c cancel") || strings.Contains(out.String(), "ctrl+b back") {
			t.Fatal("The confirmation controls do not describe cancellation.")
		}
	}
}
