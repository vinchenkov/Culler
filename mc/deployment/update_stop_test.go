package deployment

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type updateStopFixture struct {
	mu        sync.Mutex
	now       time.Time
	home      string
	processes []UpdateProcess
	active    bool
	foreign   bool
	unknown   bool
	stubborn  bool
	signals   []syscall.Signal
	calls     [][]string
}

func (f *updateStopFixture) control() UpdateStopControl {
	return UpdateStopControl{
		Now: func() time.Time { f.mu.Lock(); defer f.mu.Unlock(); return f.now },
		Wait: func(_ context.Context, d time.Duration) error {
			f.mu.Lock()
			f.now = f.now.Add(d)
			f.mu.Unlock()
			time.Sleep(time.Millisecond)
			return nil
		},
		Processes: func(context.Context) ([]UpdateProcess, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			return append([]UpdateProcess(nil), f.processes...), nil
		},
		Signal: func(_ context.Context, _ UpdateProcess, s syscall.Signal) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.signals = append(f.signals, s)
			if s == syscall.SIGKILL && !f.stubborn {
				f.processes = nil
			}
			return nil
		},
		Services: func(context.Context, bool) error { return nil },
		Docker: func(_ context.Context, args ...string) ([]byte, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.calls = append(f.calls, append([]string(nil), args...))
			if f.unknown {
				return nil, errors.New("Docker unavailable")
			}
			id := strings.Repeat("a", 64)
			switch args[0] {
			case "container":
				if args[1] == "ls" {
					if f.active {
						return []byte(id), nil
					}
					return nil, nil
				}
				names, _ := RuntimeNames(f.home)
				label := names.Suffix
				if f.foreign {
					label = "another"
				}
				return json.Marshal(map[string]any{"Id": id, "Name": "/fixture", "Config": map[string]any{"Labels": map[string]string{"mc-deployment": label}}, "State": map[string]any{"Running": f.active, "Status": "running"}})
			case "kill":
				if !f.stubborn {
					f.active = false
				}
				return nil, nil
			case "stop":
				return nil, nil
			}
			return nil, errors.New("unexpected Docker call")
		},
	}
}

func TestUpdateStopSignalsImmediatelyAndForcesAfterTenSeconds(t *testing.T) {
	f := &updateStopFixture{home: "/fixture/home", now: time.Unix(0, 0), active: true}
	f.processes = []UpdateProcess{{PID: 10, Identity: "resident", Command: "bun /app/main.ts --config /fixture/home/supervision/resident.json"}, {PID: 11, Parent: 10, Identity: "child", Command: "git write"}}
	start := f.now
	if err := StopUpdateWork(context.Background(), f.home, f.control(), nil); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.now.Sub(start) < UpdateStopGrace || f.now.Sub(start) > UpdateStopLimit {
		t.Fatalf("stop duration %s", f.now.Sub(start))
	}
	if len(f.signals) != 4 || f.signals[0] != syscall.SIGTERM || f.signals[1] != syscall.SIGTERM || f.signals[2] != syscall.SIGKILL {
		t.Fatalf("signals %v", f.signals)
	}
	stop, kill := false, false
	for _, call := range f.calls {
		if call[0] == "stop" {
			stop = true
		}
		if call[0] == "kill" {
			kill = true
		}
	}
	if !stop || !kill {
		t.Fatalf("missing stop or force: %v", f.calls)
	}
}

func TestUpdateStopRefusesUnknownOrForeignContainers(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		f := &updateStopFixture{home: "/fixture/home", now: time.Unix(0, 0), active: true, foreign: !unknown, unknown: unknown}
		if err := StopUpdateWork(context.Background(), f.home, f.control(), nil); err == nil {
			t.Fatal("unknown container state permitted replacement")
		}
		for _, call := range f.calls {
			if call[0] == "stop" || call[0] == "kill" {
				t.Fatal("a foreign container received a signal")
			}
		}
	}
}

func TestUpdateStopHasOneThirtySecondBudget(t *testing.T) {
	f := &updateStopFixture{home: "/fixture/home", now: time.Unix(0, 0), active: true, stubborn: true}
	start := f.now
	if err := StopUpdateWork(context.Background(), f.home, f.control(), nil); err == nil || !strings.Contains(err.Error(), "Replacement remains blocked") {
		t.Fatalf("error %v", err)
	}
	if f.now.Sub(start) != UpdateStopLimit {
		t.Fatalf("budget %s", f.now.Sub(start))
	}
}

func TestUpdateStopTracksReparentedChildrenAndPreservesOtherDeployments(t *testing.T) {
	retained := map[int]UpdateProcess{}
	own := UpdateProcess{PID: 10, Identity: "own", Command: "bun /fixture/home/supervision/resident.json"}
	child := UpdateProcess{PID: 11, Parent: 10, Identity: "child", Command: "git"}
	foreign := UpdateProcess{PID: 12, Identity: "foreign", Command: "bun /other/home/supervision/resident.json"}
	if rows := updateOwnedProcesses("/fixture/home", []UpdateProcess{own, child, foreign}, retained); len(rows) != 2 {
		t.Fatalf("owned %v", rows)
	}
	child.Parent = 1
	if rows := updateOwnedProcesses("/fixture/home", []UpdateProcess{child, foreign}, retained); len(rows) != 1 || rows[0].PID != 11 {
		t.Fatalf("reparented %v", rows)
	}
	child.Identity = "reused PID"
	if rows := updateOwnedProcesses("/fixture/home", []UpdateProcess{child, foreign}, retained); len(rows) != 0 {
		t.Fatalf("PID reuse included %v", rows)
	}
}

func TestUpdateStopKeepsTheServiceProcessGroupAfterItsParentExits(t *testing.T) {
	retained := map[int]UpdateProcess{}
	root := UpdateProcess{PID: 10, Group: 10, Identity: "service", Command: "bun /fixture/home/supervision/resident.json"}
	updateOwnedProcesses("/fixture/home", []UpdateProcess{root}, retained)
	orphan := UpdateProcess{PID: 11, Parent: 1, Group: 10, Identity: "new child", Command: "git"}
	foreign := UpdateProcess{PID: 12, Parent: 1, Group: 12, Identity: "other", Command: "git"}
	rows := updateOwnedProcesses("/fixture/home", []UpdateProcess{orphan, foreign}, retained)
	if len(rows) != 1 || rows[0].PID != 11 {
		t.Fatalf("process group %v", rows)
	}
	root.Identity, root.Command = "reused leader", "other application"
	if rows := updateOwnedProcesses("/fixture/home", []UpdateProcess{root, foreign}, retained); len(rows) != 0 {
		t.Fatalf("reused process group %v", rows)
	}
}
