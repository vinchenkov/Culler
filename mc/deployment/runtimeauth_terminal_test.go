//go:build darwin || linux

package deployment

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSetupRuntimeAuthLoginReadsTheSetupTerminal(t *testing.T) {
	const marker = "MC_TEST_SETUP_LOGIN_TERMINAL"
	if os.Getenv(marker) == "1" {
		environment := setupRuntimeLoginFixture(t, "claude", `
read -r answer
[ "$answer" = fixture ] || exit 1
printf '%s\n' 'https://example.test/authorize'
`)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		address := ""
		login := NewSetupRuntimeAuthLogin(ctx, func(value, _ string) { address = value })
		if err := login("claude", nil, environment, os.Stdin, io.Discard); err != nil {
			t.Fatalf("The sign-in command could not read the setup terminal: %v", err)
		}
		if address != "https://example.test/authorize" {
			t.Fatal("The sign-in command did not supply the browser address.")
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	selector := "-test.run=^TestSetupRuntimeAuthLoginReadsTheSetupTerminal$"
	args := []string{"-q", "/dev/null", os.Args[0], selector}
	if runtime.GOOS == "linux" {
		quoted := "'" + strings.ReplaceAll(os.Args[0], "'", "'\\''") + "'"
		args = []string{"-q", "-e", "-c", quoted + " " + selector, "/dev/null"}
	}
	command := exec.CommandContext(ctx, "/usr/bin/script", args...)
	command.Env = append(os.Environ(), marker+"=1")
	input, sender, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer sender.Close()
	command.Stdin = input
	command.WaitDelay = 100 * time.Millisecond
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(sender, "fixture\n"); err != nil {
		cancel()
		_ = command.Wait()
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil || !strings.Contains(output.String(), "PASS") {
		t.Fatalf("The terminal sign-in failed: %v\n%s", err, output.String())
	}
}
