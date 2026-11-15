package deployment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func foregroundBunFixture(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "bun"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestForegroundProviderCommandUsesInstalledHostRelease(t *testing.T) {
	foregroundBunFixture(t)
	home := t.TempDir()
	if err := os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallHostRelease(home, hostReleaseSourceFixture(t)); err != nil {
		t.Fatal(err)
	}
	socketHome := t.TempDir()
	command, err := foregroundProviderCommand(context.Background(), home, socketHome)
	if err != nil {
		t.Fatal(err)
	}
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(canonicalHome, "release", "host", "resident", "src", "provider-foreground.ts")
	actual, err := filepath.EvalSymlinks(command.Args[1])
	if err != nil || actual != entry {
		t.Fatalf("The proxy command does not use the installed host release: %v", err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(command.Args[2:], []string{"--home", home, "--socket-home", socketHome, "--mc", binary}) {
		t.Fatalf("The proxy command lost its deployment or lifetime arguments: %q", command.Args[2:])
	}
	bun, err := HostBunPath(home)
	if err != nil || command.Args[0] != bun {
		t.Fatalf("The proxy command does not run the selected Bun: %q, %v", command.Args[0], err)
	}
}

func TestForegroundProviderRejectsAbsentOrMisplacedRelease(t *testing.T) {
	foregroundBunFixture(t)
	for _, misplaced := range []bool{false, true} {
		t.Run(fmt.Sprint(misplaced), func(t *testing.T) {
			home := t.TempDir()
			if misplaced {
				path := filepath.Join(home, "release", "resident", "src", "provider-foreground.ts")
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("throw new Error('unexpected execution');\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := startForegroundProviderAccess(context.Background(), home)
			var startup *ProviderProxyStartError
			if !errors.As(err, &startup) || err.Error() != "The host provider proxy release is unavailable." {
				t.Fatalf("The absent host release did not report a local startup failure: %v", err)
			}
		})
	}
}

func TestForegroundProviderProcessHelper(t *testing.T) {
	if os.Getenv("MC_TEST_FOREGROUND_HELPER") != "1" {
		return
	}
	home := os.Getenv("MC_TEST_FOREGROUND_SOCKET")
	if err := os.Mkdir(filepath.Join(home, "health"), 0700); err != nil {
		os.Exit(11)
	}
	listener, err := net.Listen("unix", filepath.Join(home, "health", "provider-acceptance.sock"))
	if err != nil {
		os.Exit(12)
	}
	defer listener.Close()
	marker := os.Getenv("MC_TEST_FOREGROUND_DRAIN_MARKER")
	if marker != "" {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM)
		defer signal.Stop(signals)
	}
	if os.Getenv("MC_TEST_FOREGROUND_BAD_READY") == "1" {
		fmt.Fprintln(os.Stdout, "synthetic-private-startup-output")
	} else {
		fmt.Fprintln(os.Stdout, `{"ready":true}`)
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if marker != "" {
		time.Sleep(2300 * time.Millisecond)
		if err := os.WriteFile(marker, []byte("durable"), 0600); err != nil {
			os.Exit(13)
		}
	}
	os.Exit(0)
}

func TestForegroundCancellationAllowsAcceptedRotationToFinishAfterCallerReturns(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "rotation-saved")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	build := foregroundFixtureCommand(t, false)
	session, err := startForegroundProviderCommand(ctx, t.TempDir(), func(ctx context.Context, home, socketHome string) (*exec.Cmd, error) {
		command, err := build(ctx, home, socketHome)
		if err == nil {
			command.Env = append(command.Env, "MC_TEST_FOREGROUND_DRAIN_MARKER="+marker)
		}
		return command, err
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	started := time.Now()
	if err := session.stop(); err != nil || time.Since(started) > 2200*time.Millisecond {
		t.Fatalf("The caller waited for the complete renewal drain: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		body, readErr := os.ReadFile(marker)
		_, statErr := os.Stat(session.home)
		if readErr == nil && string(body) == "durable" && os.IsNotExist(statErr) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("The canceled temporary host lost its accepted rotation or retained its socket directory.")
}

func foregroundFixtureCommand(t *testing.T, badReady bool) func(context.Context, string, string) (*exec.Cmd, error) {
	t.Helper()
	return func(ctx context.Context, _ string, socketHome string) (*exec.Cmd, error) {
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestForegroundProviderProcessHelper$")
		command.Env = append(os.Environ(), "MC_TEST_FOREGROUND_HELPER=1", "MC_TEST_FOREGROUND_SOCKET="+socketHome)
		if badReady {
			command.Env = append(command.Env, "MC_TEST_FOREGROUND_BAD_READY=1")
		}
		return command, nil
	}
}

func TestForegroundProviderProcessUsesPrivateSocketAndStopsWithLease(t *testing.T) {
	session, err := startForegroundProviderCommand(context.Background(), t.TempDir(), foregroundFixtureCommand(t, false))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(session.home)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("The temporary socket directory is not private.")
	}
	if _, err := os.Stat(filepath.Join(session.home, "health", "provider-acceptance.sock")); err != nil {
		t.Fatal(err)
	}
	if err := session.stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(session.home); !os.IsNotExist(err) {
		t.Fatal("The temporary host retained its socket directory.")
	}
	if err := session.stop(); err != nil {
		t.Fatal("Repeated stop failed.")
	}
}

func TestForegroundProviderProcessStopsOnCancellationAndRedactsStartupErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	session, err := startForegroundProviderCommand(ctx, t.TempDir(), foregroundFixtureCommand(t, false))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	started := time.Now()
	if err := session.stop(); err != nil || time.Since(started) > 3*time.Second {
		t.Fatalf("The canceled host retained its process: %v", err)
	}
	if _, err := startForegroundProviderCommand(context.Background(), t.TempDir(), foregroundFixtureCommand(t, true)); err == nil || strings.Contains(err.Error(), "synthetic-private-startup-output") {
		t.Fatalf("The startup error exposed private output: %v", err)
	}
}

func TestProviderAccessNeverReplacesAPresentResidentSocket(t *testing.T) {
	home, _ := residentAcceptanceHome(t)
	if err := os.Chmod(filepath.Join(home, "health", "provider-acceptance.sock"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := authorizeProviderAccess(home, "connection", "profile", "model", "", 1)
	if err == nil || strings.Contains(err.Error(), "release is unavailable") {
		t.Fatalf("A failed resident triggered a temporary host: %v", err)
	}
}
