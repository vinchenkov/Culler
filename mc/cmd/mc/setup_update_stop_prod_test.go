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

func TestPublishedRestoreStartsThePreviousHelperBeforeServices(t *testing.T) {
	old := publishedUpdateStopControl
	t.Cleanup(func() { publishedUpdateStopControl = old })
	var steps []string
	publishedUpdateStopControl = func(string, func(context.Context, ...string) ([]byte, error)) deployment.UpdateStopControl {
		return deployment.UpdateStopControl{Now: time.Now, Processes: func(context.Context) ([]deployment.UpdateProcess, error) { return nil, nil },
			Services: func(context.Context, bool) error { return nil },
			Wait:     func(_ context.Context, d time.Duration) error { time.Sleep(d); return nil },
			Docker:   func(context.Context, ...string) ([]byte, error) { return nil, nil }}
	}
	for _, helperFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "helper starts", true: "helper fails"}[helperFails], func(t *testing.T) {
			steps = nil
			p := updateTransactionFixture(t, "prepare")
			previous := *p.update.Previous
			previous.Image = "ghcr.io/test/culler@sha256:" + strings.Repeat("b", 64)
			p.update.Previous, p.installed = &previous, previous
			p.docker = func(_ context.Context, args ...string) ([]byte, error) {
				if args[len(args)-1] == "__update-reconcile" {
					steps = append(steps, "stop")
				}
				return []byte(`{"runs":0,"executions":0,"sessions":0,"environments":0}`), nil
			}
			p.helper = func(home, image string) error {
				steps = append(steps, "helper "+image)
				if helperFails {
					return errors.New("image absent")
				}
				return nil
			}
			p.restore = func(string, map[string]bool) error {
				steps = append(steps, "services")
				return errors.New("services stopped for the test")
			}
			err := p.restoreUpdate(context.Background())
			want := "stop|helper " + previous.Image + "|services"
			if helperFails {
				want = "stop|helper " + previous.Image
				if err == nil || !strings.Contains(err.Error(), "helper container") {
					t.Fatalf("helper failure: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "services stopped for the test") {
				t.Fatalf("restore: %v", err)
			}
			if got := strings.Join(steps, "|"); got != want {
				t.Fatalf("steps %q, want %q", got, want)
			}
		})
	}
}

func TestUpdateDrainWindowIsTheApprovedTenMinutes(t *testing.T) {
	if updateDrainWindow != 10*time.Minute {
		t.Fatalf("the drain window changed without the owner's parameter decision: %v", updateDrainWindow)
	}
}

func TestPublishedUpdateStopDrainsBeforeInterrupting(t *testing.T) {
	old := publishedUpdateStopControl
	t.Cleanup(func() { publishedUpdateStopControl = old })
	var steps []string
	publishedUpdateStopControl = func(string, func(context.Context, ...string) ([]byte, error)) deployment.UpdateStopControl {
		return deployment.UpdateStopControl{Now: time.Now, Services: func(context.Context, bool) error { return nil },
			Processes: func(context.Context) ([]deployment.UpdateProcess, error) {
				steps = append(steps, "stop")
				return nil, nil
			},
			Wait:   func(_ context.Context, d time.Duration) error { return nil },
			Docker: func(context.Context, ...string) ([]byte, error) { return nil, nil }}
	}
	p := &publishedSetup{home: t.TempDir(), update: &deployment.UpdateJournal{ID: strings.Repeat("a", 32)},
		installed:   distribution.Record{Image: "previous@sha256:" + strings.Repeat("a", 64)},
		drainWindow: time.Minute,
		now:         func() time.Time { return time.Unix(1700000000, 0) },
		sleep:       func(time.Duration) {},
		drainIdle: func(context.Context) error {
			if len(steps) < 2 {
				steps = append(steps, "drain-busy")
				return errors.New("active work remains")
			}
			steps = append(steps, "drain-idle")
			return nil
		},
		docker: func(_ context.Context, args ...string) ([]byte, error) {
			if args[len(args)-1] == "__update-reconcile" {
				steps = append(steps, "reconcile")
				return []byte(`{"runs":0,"executions":0,"sessions":0,"environments":0}`), nil
			}
			return nil, nil
		}}
	var reports []string
	if err := p.stopForUpdate(context.Background(), func(line string) { reports = append(reports, line) }); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(steps, "|")
	if !strings.HasPrefix(joined, "drain-busy|drain-busy|drain-idle|stop") || !strings.HasSuffix(joined, "reconcile") {
		t.Fatalf("quiescence steps %q", joined)
	}
	if strings.Contains(strings.SplitN(joined, "stop", 2)[1], "drain") {
		t.Fatalf("a drain sample ran after the stop phase began: %q", joined)
	}
	if !strings.Contains(strings.Join(reports, "\n"), "Active work finished before the stop.") {
		t.Fatalf("reports %q", reports)
	}
}

func TestPublishedUpdateStopInterruptsWhenTheDrainWindowCloses(t *testing.T) {
	old := publishedUpdateStopControl
	t.Cleanup(func() { publishedUpdateStopControl = old })
	var steps []string
	publishedUpdateStopControl = func(string, func(context.Context, ...string) ([]byte, error)) deployment.UpdateStopControl {
		return deployment.UpdateStopControl{Now: time.Now, Services: func(context.Context, bool) error { return nil },
			Processes: func(context.Context) ([]deployment.UpdateProcess, error) {
				steps = append(steps, "stop")
				return nil, nil
			},
			Wait:   func(_ context.Context, d time.Duration) error { return nil },
			Docker: func(context.Context, ...string) ([]byte, error) { return nil, nil }}
	}
	start := time.Unix(1700000000, 0)
	clock := start
	p := &publishedSetup{home: t.TempDir(), update: &deployment.UpdateJournal{ID: strings.Repeat("a", 32)},
		installed:   distribution.Record{Image: "previous@sha256:" + strings.Repeat("a", 64)},
		drainWindow: 30 * time.Second,
		now:         func() time.Time { clock = clock.Add(10 * time.Second); return clock },
		sleep:       func(time.Duration) {},
		drainIdle: func(context.Context) error {
			steps = append(steps, "drain-busy")
			return errors.New("active work remains")
		},
		docker: func(_ context.Context, args ...string) ([]byte, error) {
			if args[len(args)-1] == "__update-reconcile" {
				steps = append(steps, "reconcile")
				return []byte(`{"runs":1,"executions":1,"sessions":0,"environments":0}`), nil
			}
			return nil, nil
		}}
	var reports []string
	if err := p.stopForUpdate(context.Background(), func(line string) { reports = append(reports, line) }); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(reports, "\n")
	if !strings.Contains(joined, "Active work remains. Interrupted runs can require a retry.") {
		t.Fatalf("reports %q", reports)
	}
	expiry := strings.Join(steps, "|")
	if !strings.HasPrefix(expiry, "drain-busy|drain-busy|drain-busy|stop") || !strings.HasSuffix(expiry, "reconcile") {
		t.Fatalf("expiry steps %q", expiry)
	}
	if strings.Contains(strings.SplitN(expiry, "stop", 2)[1], "drain") {
		t.Fatalf("the window kept sampling after the stop phase began: %q", expiry)
	}
}
