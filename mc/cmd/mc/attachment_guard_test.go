package main

import (
	"bufio"
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startAttachmentGuardProcessForTest(
	t *testing.T,
	generation string,
) (*io.PipeWriter, <-chan int, *bytes.Buffer) {
	t.Helper()
	t.Setenv("MC_RUN_JSON", filepath.Join(t.TempDir(), "absent-run.json"))
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()
	stderr := &bytes.Buffer{}
	done := make(chan int, 1)
	go func() {
		defer stdinReader.Close()
		done <- runAttachmentHealthGuard(
			[]string{"__attachment-health-guard"},
			stdinReader, stdoutWriter, stderr,
		)
		_ = stdoutWriter.Close()
	}()
	if _, err := io.WriteString(stdinWriter,
		`{"generation":"`+generation+`","version":1}`+"\n"); err != nil {
		t.Fatal(err)
	}
	ready, err := bufio.NewReader(stdoutReader).ReadString('\n')
	if err != nil || !strings.Contains(ready, `"state":"guarded"`) {
		t.Fatalf("guard readiness=%q err=%v stderr=%q", ready, err, stderr.String())
	}
	return stdinWriter, done, stderr
}

func startAttachmentGuardForTest(
	t *testing.T,
	generation string,
) func() {
	t.Helper()
	stdinWriter, done, stderr := startAttachmentGuardProcessForTest(t, generation)
	return func() {
		_ = stdinWriter.Close()
		if code := <-done; code != 0 {
			t.Fatalf("guard exit=%d stderr=%q", code, stderr.String())
		}
	}
}

func TestAttachmentHealthGuardMakesCanaryAuthorityBootLocal(t *testing.T) {
	generation := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	home := t.TempDir()
	writeBrokerCanaryReceipt(t, home, generation)
	t.Setenv("MC_HOME", home)
	t.Setenv("MC_RUN_JSON", "")
	t.Setenv("MC_SPINE", "")
	stop := startAttachmentGuardForTest(t, generation)
	lease, err := acquireCurrentAttachmentCanaryGeneration(generation)
	if err != nil {
		t.Fatalf("live guard refused: %v", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	stop()
	if lease, err := acquireCurrentAttachmentCanaryGeneration(generation); err == nil {
		_ = lease.Close()
		t.Fatal("persisted canary receipt remained authoritative after guard exit")
	}
}

func TestAttachmentHealthGuardDrainsAuthorizedBrokerLeaseBeforeExit(t *testing.T) {
	generation := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	home := t.TempDir()
	writeBrokerCanaryReceipt(t, home, generation)
	t.Setenv("MC_HOME", home)
	t.Setenv("MC_RUN_JSON", "")
	t.Setenv("MC_SPINE", "")
	stdinWriter, done, stderr := startAttachmentGuardProcessForTest(t, generation)

	lease, err := acquireCurrentAttachmentCanaryGeneration(generation)
	if err != nil {
		t.Fatalf("acquire broker lease: %v", err)
	}
	if err := stdinWriter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		t.Fatalf("guard exited before broker drain: code=%d stderr=%q", code, stderr.String())
	case <-time.After(100 * time.Millisecond):
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("guard exit=%d stderr=%q", code, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("guard did not exit after broker lease drained")
	}
	if lease, err := acquireCurrentAttachmentCanaryGeneration(generation); err == nil {
		_ = lease.Close()
		t.Fatal("new broker lease succeeded after guard liveness ended")
	}
}
