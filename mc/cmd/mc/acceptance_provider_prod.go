//go:build !test_fake_routing

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"mc/deployment"
	"mc/verbs"
)

type providerAcceptanceDeps struct {
	requireActive func(string) error
	runTurn       func(string, string, string, string, string) (deployment.LiveProviderObservation, error)
	cleanupTurn   func(string, string, string, string) error
	now           func() time.Time
	environment   func() []string
}

func productionProviderAcceptanceDeps() providerAcceptanceDeps {
	return providerAcceptanceDeps{
		requireActive: requireS7ActiveSupervision,
		runTurn: func(home, release, binding, attemptID, authorizationID string) (deployment.LiveProviderObservation, error) {
			image, err := configuredProductionImage()
			if err != nil {
				return deployment.LiveProviderObservation{}, err
			}
			verifier := deployment.NewAdapterNoopVerifier(home, image)
			return verifier.VerifyLive(
				binding, filepath.Join(home, "refresh-grants"), attemptID, authorizationID)
		},
		now: time.Now, environment: os.Environ,
	}
}

func executeProviderAcceptance(home, release, mode, binding, attemptID string, deps providerAcceptanceDeps) (deployment.LiveProviderResult, error) {
	switch mode {
	case "status":
		return deployment.StatusLiveProviderAcceptance(home, release, binding)
	case "cleanup":
		return deployment.CleanupLiveProviderAcceptance(
			home, release, binding, attemptID,
			func() error {
				if deps.cleanupTurn == nil {
					return nil
				}
				if err := deps.cleanupTurn(home, release, binding, attemptID); err != nil {
					return fmt.Errorf("resident acceptance cleanup: %w", err)
				}
				return nil
			},
		)
	case "run":
		if replayed, ok, err := deployment.ReplayLiveProviderAcceptance(home, release, binding); err != nil {
			return deployment.LiveProviderResult{}, err
		} else if ok {
			return replayed, nil
		}
		status, err := deployment.StatusLiveProviderAcceptance(home, release, binding)
		if err != nil {
			return deployment.LiveProviderResult{}, err
		}
		if status.Status == "running" {
			return deployment.LiveProviderResult{}, fmt.Errorf(
				"live-provider acceptance is ambiguous after a prior start; inspect provider usage, then run explicit cleanup before any retry")
		}
		if err := deps.requireActive(home); err != nil {
			return deployment.LiveProviderResult{}, fmt.Errorf("supervision check: %w", err)
		}
		claim, err := deployment.BeginLiveProviderAcceptance(
			home, release, binding, deps.environment(), deps.now())
		if err != nil {
			return deployment.LiveProviderResult{}, err
		}
		defer claim.Close()
		observation, err := deps.runTurn(
			home, release, binding, claim.AttemptID(), claim.AuthorizationID())
		if err != nil {
			return deployment.LiveProviderResult{}, fmt.Errorf(
				"live provider attempt is now ambiguous and will not retry automatically: %w", err)
		}
		return deployment.CompleteLiveProviderAcceptance(claim, observation, deps.now())
	default:
		return deployment.LiveProviderResult{}, fmt.Errorf("unsupported provider acceptance mode %q", mode)
	}
}

func brokerProviderAcceptance(args []string, stdout, stderr io.Writer) int {
	if len(args) < 3 || args[0] != "__acceptance" || args[1] != "provider" {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("usage: mc __acceptance provider <run|status|cleanup> --binding <binding> [--attempt <id>] [--confirm-live-spend]"))
	}
	mode := args[2]
	rest := args[3:]
	binding := ""
	attemptID := ""
	confirmed := false
	for len(rest) > 0 {
		switch rest[0] {
		case "--binding":
			if binding != "" || len(rest) < 2 || rest[1] == "" {
				return writeVerbError(stdout, stderr,
					verbs.Usagef("mc __acceptance provider requires exactly one --binding"))
			}
			binding = rest[1]
			rest = rest[2:]
		case "--confirm-live-spend":
			if confirmed {
				return writeVerbError(stdout, stderr,
					verbs.Usagef("--confirm-live-spend may appear only once"))
			}
			confirmed = true
			rest = rest[1:]
		case "--attempt":
			if attemptID != "" || len(rest) < 2 || rest[1] == "" {
				return writeVerbError(stdout, stderr,
					verbs.Usagef("mc __acceptance provider requires exactly one --attempt"))
			}
			attemptID = rest[1]
			rest = rest[2:]
		default:
			return writeVerbError(stdout, stderr,
				verbs.Usagef("unknown provider acceptance argument %q", rest[0]))
		}
	}
	if binding == "" || (mode != "run" && mode != "status" && mode != "cleanup") ||
		(mode == "run" && (!confirmed || attemptID != "")) ||
		(mode == "status" && (confirmed || attemptID != "")) ||
		(mode == "cleanup" && (confirmed || attemptID == "")) {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("usage: mc __acceptance provider <run|status|cleanup> --binding <binding> [--attempt <id>] [--confirm-live-spend]"))
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("resolve provider acceptance MC_HOME: %v", err))
	}
	result, err := executeProviderAcceptance(
		home, releaseBuildID, mode, binding, attemptID, productionProviderAcceptanceDeps())
	if err != nil {
		return writeVerbError(stdout, stderr,
			verbs.Domainf("provider acceptance %s: %v", mode, err))
	}
	if err := writeJSON(stdout, result); err != nil {
		fmt.Fprintln(stderr, "mc:", err)
		return 2
	}
	return 0
}
