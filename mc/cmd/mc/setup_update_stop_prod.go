//go:build !test_fake_routing

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"mc/deployment"
	"mc/verbs"
)

var publishedUpdateStopControl = deployment.NewUpdateStopControl

func (p *publishedSetup) stopForUpdate(ctx context.Context, report func(string)) error {
	if p.update == nil || p.update.ID == "" {
		return errors.New("The update requires its recovery record before services stop.")
	}
	ctx, cancel := context.WithTimeout(ctx, deployment.UpdateStopLimit)
	defer cancel()
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
	body, err := p.docker(ctx, "run", "--rm", "--network", "none", "--cap-drop", "ALL", "--user", "10002:10002",
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
