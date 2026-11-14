package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"mc/deployment"
	"mc/distribution"
	"mc/substrate"
	"mc/verbs"
)

func operatorDeploymentGuard(mutates bool, release string) error {
	if !mutates {
		return nil
	}
	if release != releaseBuildID {
		return &verbs.DomainError{Code: "release-changed", Msg: "The request and the host use different Culler releases."}
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return err
	}
	if err := deployment.UpdateBlocksWork(home); err != nil {
		return &verbs.DomainError{Code: "update-incomplete", Msg: err.Error()}
	}
	if _, err := os.Lstat(filepath.Join(home, "state", "install-maintenance.json")); err == nil || !errors.Is(err, os.ErrNotExist) {
		return &verbs.DomainError{Code: "update-incomplete", Msg: "Culler is in install maintenance. Complete the installation before a new change."}
	}
	var runtime struct {
		Release string `json:"releaseBuildId"`
		Home    string `json:"mcHome"`
	}
	body, err := os.ReadFile(filepath.Join(home, "supervision", "resident.json"))
	if errors.Is(err, os.ErrNotExist) && directPrivateAttachmentOK() {
		return nil
	}
	if err != nil || json.Unmarshal(body, &runtime) != nil || runtime.Home != home || runtime.Release == "" {
		return &verbs.DomainError{Code: "deployment-unavailable", Msg: "The selected Culler deployment is unavailable. Complete its installation before a new change."}
	}
	if runtime.Release != release {
		return &verbs.DomainError{Code: "release-changed", Msg: "The command and installed services use different Culler releases."}
	}
	installed, err := distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || installed.Commit != release || installed.Schema != substrate.CurrentSchemaID {
		return &verbs.DomainError{Code: "release-changed", Msg: "The installed release and command are incompatible. Complete the Culler installation before a new change."}
	}
	return nil
}
