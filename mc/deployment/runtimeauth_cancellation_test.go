package deployment

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdapterNoopVerifierRejectsCancellationBeforeWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	verifier := &AdapterNoopVerifier{Image: "fixture-image", Context: ctx}
	if err := verifier.Verify("absent", "absent"); !errors.Is(err, context.Canceled) {
		t.Fatalf("The verifier did not reject cancellation before file access: %v", err)
	}
}

func TestAdapterNoopVerifierCancelsHostAuthorization(t *testing.T) {
	home, listener := residentAcceptanceHome(t)
	requested := make(chan struct{})
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer conn.Close()
		var data [4096]byte
		_, _ = conn.Read(data[:])
		close(requested)
		_, _ = conn.Read(data[:])
	}()
	verifier, stage, _ := verifierFixture(t, "codex", "account", 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	verifier.Home = home
	verifier.Context = ctx
	verifier.Access = nil
	verifier.Run = func([]string, string) (runtimeAuthOutput, error) {
		t.Error("A canceled host request reached the adapter.")
		return runtimeAuthOutput{}, nil
	}
	done := make(chan error, 1)
	go func() { done <- verifier.Verify("chatgpt", stage) }()
	select {
	case <-requested:
	case <-time.After(3 * time.Second):
		t.Fatal("The verifier did not request host authorization.")
	}
	cancel()
	if err := waitRuntimeAuthCancellation(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("The host request lost cancellation: %v", err)
	}
	assertRuntimeAuthVerifierScratchEmpty(t, filepath.Dir(stage))
}

func TestAdapterNoopVerifierRevokesLateAuthorizationAfterCancellation(t *testing.T) {
	verifier, stage, access := verifierFixture(t, "codex", "account", 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	verifier.Context = ctx
	verifier.Access = func(connection, profile, model string, maximum int) (*providerAccessLease, error) {
		lease, err := access.access(connection, profile, model, maximum)
		cancel()
		return lease, err
	}
	verifier.Run = func([]string, string) (runtimeAuthOutput, error) {
		t.Fatal("A canceled authorization reached the adapter.")
		return runtimeAuthOutput{}, nil
	}
	if err := verifier.Verify("chatgpt", stage); !errors.Is(err, context.Canceled) {
		t.Fatalf("The late authorization lost cancellation: %v", err)
	}
	calls := access.recorded()
	if len(calls) != 2 || calls[1]["op"] != "profile_revoke" {
		t.Fatalf("The late authorization remained active: %v", calls)
	}
	assertRuntimeAuthVerifierScratchEmpty(t, filepath.Dir(stage))
}

func TestAdapterNoopVerifierCancelsDockerAndRemovesScratch(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		name := "cleanup succeeds"
		if cleanupFails {
			name = "cleanup fails"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			tools := filepath.Join(root, "tools")
			if err := os.Mkdir(tools, 0700); err != nil {
				t.Fatal(err)
			}
			body := `#!/bin/sh
set -eu
case "$1" in
  run)
    [ "$2" = --name ]
    printf '%s' "$3" > "$MC_TEST_AUTH_DOCKER_ROOT/container-name"
    printf '%s\n' "$@" > "$MC_TEST_AUTH_DOCKER_ROOT/arguments"
    : > "$MC_TEST_AUTH_DOCKER_ROOT/started"
    exec /bin/sleep 30
    ;;
  rm)
    [ "$2" = --force ]
    [ "$3" = "$(cat "$MC_TEST_AUTH_DOCKER_ROOT/container-name")" ]
    : > "$MC_TEST_AUTH_DOCKER_ROOT/removed"
    if [ "${MC_TEST_AUTH_CLEANUP_FAIL:-0}" = 1 ]; then
      printf 'PRIVATE_CLEANUP_OUTPUT\n' >&2
      exit 19
    fi
    ;;
  *) exit 20 ;;
esac
`
			if err := os.WriteFile(filepath.Join(tools, "docker"), []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", tools+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("MC_TEST_AUTH_DOCKER_ROOT", root)
			if cleanupFails {
				t.Setenv("MC_TEST_AUTH_CLEANUP_FAIL", "1")
			} else {
				t.Setenv("MC_TEST_AUTH_CLEANUP_FAIL", "0")
			}
			stage := filepath.Join(root, "stage")
			if err := os.Mkdir(stage, 0700); err != nil {
				t.Fatal(err)
			}
			stagedVerifierGrant(t, stage, "endpoint")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			verifier := &AdapterNoopVerifier{
				Image:   "fixture-image",
				Context: ctx, Catalog: signInCatalog(t), RunnerSourceDir: verifierRunnerSource(t, root),
				Access: newProviderAccessFixture(t, "claude-sdk", "account", 1).access,
			}
			done := make(chan error, 1)
			go func() { done <- verifier.Verify("endpoint", stage) }()
			waitRuntimeAuthFixtureFile(t, filepath.Join(root, "started"))
			cancel()
			err := waitRuntimeAuthCancellation(t, done)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("The Docker command lost cancellation: %v", err)
			}
			var cleanup *RuntimeAuthCleanupError
			if errors.As(err, &cleanup) != cleanupFails || strings.Contains(err.Error(), "PRIVATE_CLEANUP_OUTPUT") {
				t.Fatalf("The cleanup result lost its safe diagnostic: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "removed")); err != nil {
				t.Fatalf("The canceled verifier did not remove its scratch container: %v", err)
			}
			arguments, err := os.ReadFile(filepath.Join(root, "arguments"))
			if err != nil || !strings.Contains(string(arguments), "\n--rm\n") {
				t.Fatalf("The Docker command lost automatic removal: %v", err)
			}
			assertRuntimeAuthVerifierScratchEmpty(t, root)
		})
	}
}

func TestAdapterNoopVerifierCancellationPreservesSavedNativeConnection(t *testing.T) {
	home, stage := selectedRuntimeAuthFixture(t)
	publishSelectedRuntimeFixture(t, home, "chatgpt", "previous-refresh")
	identity, err := AgentConnectionAuthorizationIdentity(home, "chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	verifier := &AdapterNoopVerifier{
		Image:   "fixture-image",
		Context: ctx, Catalog: signInCatalog(t), RunnerSourceDir: verifierRunnerSource(t, home),
		Access: newProviderAccessFixture(t, "codex", "account", 1).access,
		Run: func(argv []string, _ string) (runtimeAuthOutput, error) {
			session := mountSource(argv, "/mc/session")
			if err := os.WriteFile(filepath.Join(session, "native.jsonl"), []byte("{}\n"), 0600); err != nil {
				return runtimeAuthOutput{}, err
			}
			cancel()
			return runtimeAuthOutput{Stdout: []byte(`{"event":"session-start","session_id":"fixture","native_file":"native.jsonl"}` + "\n")}, nil
		},
	}
	status, err := ImportSelectedStagedRuntimeAuth(home, stage, verifier)
	if status != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("The import accepted a canceled adapter result: %q, %v", status, err)
	}
	assertRuntimeAuthAuthorizationChanged(t, home, identity)
	assertSelectedRuntimeScratchEmpty(t, home)
}

type scopedRuntimeAuthVerifier struct {
	Context context.Context
	RuntimeAuthVerifyFunc
}

func (v scopedRuntimeAuthVerifier) VerificationContext() context.Context { return v.Context }

func TestImportSelectedRuntimeAuthCancellationPreservesHostRenewal(t *testing.T) {
	home, stage := selectedRuntimeAuthFixture(t)
	publishSelectedRuntimeFixture(t, home, "chatgpt", "previous-refresh")
	identity, err := AgentConnectionAuthorizationIdentity(home, "chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	verifier := scopedRuntimeAuthVerifier{Context: ctx, RuntimeAuthVerifyFunc: func(_ string, root string) error {
		rotateSelectedRuntimeFixture(t, root, "canceled-refresh")
		cancel()
		return nil
	}}
	status, err := ImportSelectedStagedRuntimeAuth(home, stage, verifier)
	if status != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("The import omitted the publication check: %q, %v", status, err)
	}
	assertRuntimeAuthAuthorizationChanged(t, home, identity)
	assertSelectedRuntimeScratchEmpty(t, home)
}

func TestImportSelectedRuntimeAuthCancelsConnectionLockWait(t *testing.T) {
	home, stage := selectedRuntimeAuthFixture(t)
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := agentProfileOperationLock(root, "connection-chatgpt")
	if err != nil {
		t.Fatal(err)
	}
	defer agentProfileUnlock(lock)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var called atomic.Bool
	verifier := scopedRuntimeAuthVerifier{Context: ctx, RuntimeAuthVerifyFunc: func(string, string) error {
		called.Store(true)
		return nil
	}}
	done := make(chan error, 1)
	go func() {
		_, err := ImportSelectedStagedRuntimeAuth(home, stage, verifier)
		done <- err
	}()
	if err := waitRuntimeAuthCancellation(t, done); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("The credential lock did not honor its deadline: %v", err)
	}
	if called.Load() {
		t.Fatal("The verifier passed a connection lock that another process holds.")
	}
	assertSelectedRuntimeScratchEmpty(t, home)
}

func assertRuntimeAuthAuthorizationChanged(t *testing.T, home, identity string) {
	t.Helper()
	current, err := ReadProviderCredential(home, "chatgpt")
	if err != nil || current.AuthorizationID == identity || current.Record.RefreshToken == "previous-refresh" {
		t.Fatalf("The canceled model check lost the saved native connection: %v", err)
	}
}

func assertRuntimeAuthVerifierScratchEmpty(t *testing.T, root string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, ".runtime-auth-verify-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("The verifier retained scratch files: %v", err)
	}
}

func waitRuntimeAuthCancellation(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal("The credential test did not return after cancellation.")
		return nil
	}
}

func waitRuntimeAuthFixtureFile(t *testing.T, path string) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatal("The fake Docker command did not start.")
		}
	}
}
