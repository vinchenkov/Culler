package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDelegateAttachesInputOnlyWhenTheCallerSuppliesIt(t *testing.T) {
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()

	cases := []struct {
		name     string
		stdin    interface{ Read([]byte) (int, error) }
		attached bool
	}{
		{"no reader", nil, false},
		{"the null device", null, false},
		{"an empty body", bytes.NewReader(nil), false},
		{"an empty string", strings.NewReader(""), false},
		{"a body", bytes.NewReader([]byte(`{"id":"request-1"}`)), true},
		{"an open pipe", reader, true},
	}
	for _, c := range cases {
		input := delegateInput(c.stdin)
		argv := delegateArgv("mc-helper-test", []string{"__operator-due"}, input)
		if got := slices.Contains(argv, "-i"); got != c.attached {
			t.Errorf("%s: input attached = %v, want %v (argv %v)", c.name, got, c.attached, argv)
		}
		if (input != nil) != c.attached {
			t.Errorf("%s: forwarded input present = %v, want %v", c.name, input != nil, c.attached)
		}
		want := []string{"mc-helper-test", "mc", "__operator-due"}
		if tail := argv[len(argv)-len(want):]; !slices.Equal(tail, want) {
			t.Errorf("%s: argv ends with %v, want %v", c.name, tail, want)
		}
	}
}

func TestDelegateStopsAtItsTimeLimit(t *testing.T) {
	bin := t.TempDir()
	// The client never returns from an exec, like a docker client whose
	// container process waits for input.
	script := "#!/bin/sh\n[ \"$1\" = exec ] || exit 1\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	previous := delegateWallTimeout
	delegateWallTimeout = 300 * time.Millisecond
	defer func() { delegateWallTimeout = previous }()

	var stdout, stderr bytes.Buffer
	started := time.Now()
	code := delegate([]string{"task", "list"}, nil, &stdout, &stderr)
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("delegate returned after %s", elapsed)
	}
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout %q stderr %q)", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), `"code":"usage"`) || !strings.Contains(stdout.String(), "self-delegation exceeded") {
		t.Fatalf("stdout carries no time limit envelope: %q", stdout.String())
	}
}
