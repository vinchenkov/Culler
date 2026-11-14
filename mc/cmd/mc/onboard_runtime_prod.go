//go:build !test_fake_routing

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"mc/deployment"
	"mc/verbs"
)

// Tests replace this seam. Production constructs the real verifier only after
// canonical MC_HOME is known, because installed runner assets are home-owned.
var productionRuntimeAuthVerifier deployment.RuntimeAuthVerifier
var productionRuntimeAuthLogin deployment.RuntimeAuthLoginFunc
var productionRuntimeAuthImport = deployment.ImportStagedRuntimeAuth
var productionSelectedRuntimeAuthImport = deployment.ImportSelectedStagedRuntimeAuth

// runtimeAuthVerifier tests a connection in the agent image of the deployment.
func runtimeAuthVerifier(ctx context.Context, home string) (deployment.RuntimeAuthVerifier, error) {
	if productionRuntimeAuthVerifier != nil {
		return productionRuntimeAuthVerifier, nil
	}
	image, err := configuredProductionImage()
	if err != nil {
		return nil, err
	}
	verifier := deployment.NewAdapterNoopVerifier(home, image)
	verifier.Context = ctx
	verifier.SelectDefaultModel = true
	return verifier, nil
}

// selectedRuntimeBindings returns the explicit agent profile selection.
func selectedRuntimeBindings(raw string) ([]string, error) {
	parts := []string{}
	if raw != "" {
		parts = strings.Split(raw, ",")
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if len(parts) == 0 {
		return nil, verbs.Usagef("mc onboard runtime-auth requires --runtime-bindings; one binding is enough")
	}
	for i := range parts {
		if parts[i] == "" {
			return nil, verbs.Usagef("--runtime-bindings contains an empty binding id. Remove the extra comma.")
		}
	}
	return parts, nil
}

func brokerOnboardRuntimeAuth(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	a, err := parseOnboardArgs(args[1:])
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if a.Section != "runtime-auth" {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc onboard runtime-auth [source flags]"))
	}
	bindings, err := selectedRuntimeBindings(a.RuntimeBindings)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("resolve MC_HOME: %v", err))
	}
	if err := verbs.CheckOnboardRuntimeBindings(strings.Join(bindings, ",")); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if _, err := verbs.EnsureOnboardRuntimeCatalog(); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	catalog, err := deployment.LoadRuntimeCatalog(home)
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("load runtime catalog: %v", err))
	}
	verifier, err := runtimeAuthVerifier(context.Background(), home)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	sources := deployment.RuntimeAuthSources{
		Bindings: bindings, Environment: os.Environ(),
	}
	wantsCodex, wantsClaude, err := catalog.OAuthKindsOf(bindings)
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf("runtime-auth selection refused: %v", err))
	}
	if (wantsCodex || wantsClaude) && !a.AcquireRuntimeAuth {
		return writeVerbError(stdout, stderr, verbs.Usagef("a subscription binding requires a Culler sign-in: add --acquire (--acquire-runtime-auth for install.sh)"))
	}
	var acquisition *deployment.RuntimeAuthAcquisition
	if a.AcquireRuntimeAuth {
		acquisition, err = deployment.AcquireRuntimeAuth(home, bindings, stdin, stderr, sources.Environment, productionRuntimeAuthLogin)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf("runtime-auth acquisition refused: %v", err))
		}
	}
	staged, stageErr := deployment.StageRuntimeAuthSources(home, sources, acquisition)
	if stageErr != nil {
		var cleanupErr error
		if acquisition != nil {
			cleanupErr = acquisition.Cleanup()
		}
		if cleanupErr != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf(
				"runtime-auth source staging refused: %v; isolated source cleanup also failed: %v",
				stageErr, cleanupErr))
		}
		return writeVerbError(stdout, stderr, verbs.Domainf(
			"runtime-auth source staging refused: %v", stageErr))
	}
	if acquisition != nil {
		acquisitionCleanupErr := acquisition.Cleanup()
		acquisition = nil
		if acquisitionCleanupErr != nil {
			stageCleanupErr := staged.Cleanup()
			cleanupErr := errors.Join(acquisitionCleanupErr, stageCleanupErr)
			return writeVerbError(stdout, stderr, verbs.Domainf(
				"runtime-auth source staging refused: isolated source cleanup failed before import: %v",
				cleanupErr))
		}
	}
	status, importErr := productionRuntimeAuthImport(home, staged, verifier)
	cleanupErr := staged.Cleanup()
	if importErr != nil {
		if cleanupErr != nil {
			return writeVerbError(stdout, stderr, verbs.Domainf(
				"runtime-auth import refused: %v; isolated source cleanup also failed: %v",
				importErr, cleanupErr))
		}
		return writeVerbError(stdout, stderr, verbs.Domainf("runtime-auth import refused: %v", importErr))
	}
	if cleanupErr != nil {
		return writeVerbError(stdout, stderr, verbs.Domainf(
			"runtime-auth grants were published but isolated source cleanup failed: %v",
			cleanupErr))
	}
	configChanged, err := verbs.RecordOnboardRuntimeBindings(strings.Join(bindings, ","))
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if configChanged {
		status = "done"
	}
	return writeOnboardSection(stdout, stderr, "runtime-auth", status,
		fmt.Sprintf("%d binding grants passed forbidden-env and live no-op gates; config.toml enablement is authoritative", len(bindings)))
}

func brokerOnboardContainer(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "__onboard" || args[1] != "container" {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __onboard container"))
	}
	result, err := productionDoctorResult()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	findings, err := doctorFindings(result)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	for _, finding := range findings {
		if (finding.Check == "spine" || finding.Check == "container-runtime") && finding.Status != "ok" {
			return writeVerbError(stdout, stderr, verbs.Domainf("container onboarding failed %s: %s", finding.Check, finding.Detail))
		}
	}
	return writeOnboardSection(stdout, stderr, "container", "ok",
		"exact native image/helper envelope and kernel capability probe are healthy")
}

func brokerOnboardVerify(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "__onboard" || args[1] != "verify" {
		return writeVerbError(stdout, stderr, verbs.Usagef("usage: mc __onboard verify"))
	}
	result, err := productionDoctorResult()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	findings, err := doctorFindings(result)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	detail, err := verbs.OnboardVerification(findings)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	return writeOnboardSection(stdout, stderr, "verify", "ok", detail)
}
