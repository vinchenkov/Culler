//go:build !test_fake_routing

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"mc/deployment"
	"mc/distribution"
)

func TestPublishedUpdateStopPreservesMaintenanceAfterUnknownExit(t *testing.T) {
	old := publishedUpdateStopControl
	t.Cleanup(func() { publishedUpdateStopControl = old })
	publishedUpdateStopControl = func(string, func(context.Context, ...string) ([]byte, error)) deployment.UpdateStopControl {
		return deployment.UpdateStopControl{Now: time.Now, Services: func(context.Context, bool) error { return nil }, Processes: func(context.Context) ([]deployment.UpdateProcess, error) {
			return nil, errors.New("process inventory unavailable")
		}}
	}
	home := t.TempDir()
	p := &publishedSetup{home: home, update: &deployment.UpdateJournal{ID: strings.Repeat("a", 32)}, docker: func(context.Context, ...string) ([]byte, error) {
		t.Fatal("unknown exit permitted a database write")
		return nil, nil
	}}
	if err := p.stopForUpdate(context.Background(), nil); err == nil {
		t.Fatal("unknown exit succeeded")
	}
	if body, err := os.ReadFile(filepath.Join(home, "state", "install-maintenance.json")); err != nil || !strings.Contains(string(body), p.update.ID) {
		t.Fatalf("maintenance %q %v", body, err)
	}
}

func TestPublishedUpdateStopUsesThePreviousImageAfterVerifiedExit(t *testing.T) {
	old := publishedUpdateStopControl
	t.Cleanup(func() { publishedUpdateStopControl = old })
	publishedUpdateStopControl = func(string, func(context.Context, ...string) ([]byte, error)) deployment.UpdateStopControl {
		return deployment.UpdateStopControl{Now: time.Now, Services: func(context.Context, bool) error { return nil }, Processes: func(context.Context) ([]deployment.UpdateProcess, error) { return nil, nil },
			Signal: func(context.Context, deployment.UpdateProcess, syscall.Signal) error {
				t.Fatal("unexpected process")
				return nil
			},
			Wait:   func(_ context.Context, d time.Duration) error { time.Sleep(d); return nil },
			Docker: func(context.Context, ...string) ([]byte, error) { return nil, nil }}
	}
	home := t.TempDir()
	calls := 0
	p := &publishedSetup{home: home, installed: distribution.Record{Image: "previous@sha256:" + strings.Repeat("a", 64)}, record: distribution.Record{Image: "candidate"}, update: &deployment.UpdateJournal{ID: strings.Repeat("a", 32)}}
	p.docker = func(_ context.Context, args ...string) ([]byte, error) {
		calls++
		if args[len(args)-3] != p.installed.Image || args[len(args)-1] != "__update-reconcile" {
			t.Fatalf("reconciliation %v", args)
		}
		if !strings.Contains(strings.Join(args, " "), "mc-deployment=") {
			t.Fatal("reconciliation container lacks its deployment identity")
		}
		return []byte(`{"runs":1,"executions":1,"sessions":1,"environments":0}`), nil
	}
	if err := p.stopForUpdate(context.Background(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("reconcile calls %d", calls)
	}
}

func TestPublishedRestoreStopsWithoutOpeningTheDamagedDatabase(t *testing.T) {
	old := publishedUpdateStopControl
	t.Cleanup(func() { publishedUpdateStopControl = old })
	publishedUpdateStopControl = func(string, func(context.Context, ...string) ([]byte, error)) deployment.UpdateStopControl {
		return deployment.UpdateStopControl{Now: time.Now, Services: func(context.Context, bool) error { return nil }, Processes: func(context.Context) ([]deployment.UpdateProcess, error) { return nil, nil },
			Wait: func(_ context.Context, d time.Duration) error { time.Sleep(d); return nil }, Docker: func(context.Context, ...string) ([]byte, error) { return nil, nil }}
	}
	p := &publishedSetup{home: t.TempDir(), update: &deployment.UpdateJournal{ID: strings.Repeat("a", 32), Phase: "restore", Checkpoint: &deployment.UpdateCheckpoint{}},
		docker: func(context.Context, ...string) ([]byte, error) {
			t.Fatal("restoration opened the damaged database before its checkpoint restore")
			return nil, nil
		}}
	if err := p.stopForUpdate(context.Background(), func(string) {}); err != nil {
		t.Fatal(err)
	}
}
