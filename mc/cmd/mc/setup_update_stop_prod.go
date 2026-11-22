//go:build !test_fake_routing

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	operatorconfig "mc/config"
	"os"
	"path/filepath"
	"time"

	"mc/deployment"
	"mc/verbs"
)

var publishedUpdateStopControl = deployment.NewUpdateStopControl

// The approved drain window for update interruption: the stop step waits
// this long for active work to reach its natural end before the approved
// interruption path records what remains.
const updateDrainWindow = 10 * time.Minute

// waitForDrain samples the spine and the agent containers until the window
// closes. Expiry never aborts the update: it falls through to the stop and
// reconciliation path the operator already consented to.
func (p *publishedSetup) waitForDrain(ctx context.Context, report func(string)) {
	now, sleep, idle := p.now, p.sleep, p.drainIdle
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = time.Sleep
	}
	if idle == nil {
		idle = p.workQuiesced
	}
	deadline := now().Add(p.drainWindow)
	report(fmt.Sprintf("Waiting up to %d minutes for active work to finish", int(p.drainWindow.Minutes())))
	for {
		if idle(ctx) == nil {
			report("Active work finished before the stop.")
			return
		}
		if !now().Before(deadline) {
			report("Active work remains. Interrupted runs can require a retry.")
			return
		}
		if ctx.Err() != nil {
			return
		}
		sleep(time.Second)
	}
}

func (p *publishedSetup) workQuiesced(ctx context.Context) error {
	database, err := p.database(ctx, false)
	if err != nil {
		return err
	}
	if !database.Idle() {
		return errors.New("active work remains")
	}
	return p.requireIdleContainers(ctx, database.CollieContainers...)
}

func (p *publishedSetup) stopForUpdate(ctx context.Context, report func(string)) error {
	if p.update == nil || p.update.ID == "" {
		return errors.New("The update requires its recovery record before services stop.")
	}
	p.maintenanceID = p.update.ID
	state := filepath.Join(p.home, "state")
	if err := os.MkdirAll(state, 0700); err != nil {
		return err
	}
	request, _ := json.Marshal(map[string]any{"id": p.maintenanceID, "requested_at": time.Now().UTC()})
	path := filepath.Join(state, "install-maintenance.json")
	stage, err := os.CreateTemp(state, ".install-maintenance-")
	if err != nil {
		return err
	}
	defer os.Remove(stage.Name())
	if _, err := stage.Write(append(request, '\n')); err != nil {
		stage.Close()
		return err
	}
	if err := stage.Sync(); err != nil {
		stage.Close()
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	if err := os.Rename(stage.Name(), path); err != nil {
		return err
	}
	if p.drainWindow > 0 {
		p.waitForDrain(ctx, report)
	}
	ctx, cancel := context.WithTimeout(ctx, deployment.UpdateStopLimit)
	defer cancel()
	if err := deployment.StopUpdateWork(ctx, p.home, publishedUpdateStopControl(p.home, p.docker), report); err != nil {
		return err
	}
	if p.update.Phase == "restore" && p.update.Checkpoint != nil {
		return nil
	}
	names, err := deployment.RuntimeNames(p.home)
	if err != nil {
		return err
	}
	if p.installed.Image == "" {
		return errors.New("The update requires the previous image to record interrupted work.")
	}
	report("Record interrupted work")
	body, err := p.docker(ctx, "run", "--rm", operatorconfig.PidsLimitFlag, "--network", "none", "--cap-drop", "ALL", "--user", "10002:10002",
		"--label", "mc-managed=true", "--label", "mc-deployment="+names.Suffix,
		"--mount", "type=volume,src="+names.Volume+",dst=/mc/spine", p.installed.Image, "mc", "__update-reconcile")
	if err != nil {
		return &setupFailure{cause: "Culler could not record interrupted work. Replacement remains blocked.", details: []string{setupSafeDiagnostic(string(body))}}
	}
	var result *verbs.UpdateReconciliation
	if err := json.Unmarshal(body, &result); err != nil || result == nil {
		return errors.New("The update received an invalid interruption record. Replacement remains blocked.")
	}
	return nil
}
