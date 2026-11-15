//go:build !test_fake_routing

package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"mc/deployment"
	"mc/distribution"
)

// downloadSetupAgentsView installs the pinned AgentsView executable for a
// setup from a source checkout, which has no package copy.
func downloadSetupAgentsView(home string) error {
	pin, err := deployment.LoadAgentsViewPin()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), onboardAgentsViewTimeout)
	defer cancel()
	_, _, err = deployment.EnsureAgentsView(ctx, home, pin, deployment.AgentsViewDownload{})
	return err
}

func (p *publishedSetup) installAgentsView() error {
	_, _, err := deployment.InstallAgentsViewExecutable(p.home, filepath.Join(p.root, "bin", "agentsview"), p.agentsView)
	return err
}

func agentsViewSetupFailure(err error) error {
	if err == nil {
		return nil
	}
	var failure *setupFailure
	if errors.As(err, &failure) {
		return err
	}
	details := []string{"Diagnostic: " + setupSafeDiagnostic(err.Error())}
	var mismatch *deployment.AgentsViewMismatchError
	if errors.As(err, &mismatch) {
		return &setupFailure{cause: "AgentsView failed its digest check. Run install.sh again.", details: details}
	}
	return &setupFailure{cause: "The AgentsView installation failed. Open the error details, then select Retry.", details: details}
}

// checkPublishedAgentsView requires the release record and the package
// executable to match the pin of this setup program.
func checkPublishedAgentsView(root string, r distribution.Record, pin deployment.AgentsViewPin) error {
	a := r.AgentsView
	if a == nil {
		return errors.New("The release record has no AgentsView entry. Download install.sh again.")
	}
	if a.Version != pin.Version || a.ArchiveSHA256 != pin.Archive.SHA256 || a.ExecutableSHA256 != pin.Executable.SHA256 {
		return errors.New("The release record names a different AgentsView release. Download install.sh again.")
	}
	err := deployment.VerifyAgentsViewExecutable(filepath.Join(root, "bin", "agentsview"), pin)
	var mismatch *deployment.AgentsViewMismatchError
	if errors.As(err, &mismatch) {
		return fmt.Errorf("AgentsView failed its digest check. Download install.sh again. Expected SHA-256: %s. Found SHA-256: %s.",
			mismatch.ExpectedSHA256, mismatch.ActualSHA256)
	}
	return err
}

// publishedAgentsViewFailure states the result of a failed AgentsView install
// of an automatic setup. The install error is the diagnostic.
func publishedAgentsViewFailure(err error, servicesStopped bool) error {
	cause := "The AgentsView installation failed. Run install.sh again."
	if servicesStopped {
		cause = "The AgentsView installation failed. The Culler services are stopped. Run install.sh again."
	}
	return fmt.Errorf("%s Diagnostic: %w", cause, err)
}
