//go:build !test_fake_routing

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mc/deployment"
	"mc/verbs"
)

const privateOnboardStateOutputLimit = 1 << 20

var productionOnboardGitMutation verbs.OnboardGitMutation = runProductionOnboardGitMutation
var productionOnboardGitDockerRun = func(args []string) (string, string, error) {
	var out, errOut boundedDoctorBuffer
	out.limit, errOut.limit = privateDoctorOutputLimit, privateDoctorOutputLimit
	cmd := exec.Command("docker", args...)
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if out.overflow || errOut.overflow {
		return "", "", verbs.Domainf("workspace Git preparation output exceeded its bound")
	}
	return strings.TrimSpace(string(out.Bytes())), strings.TrimSpace(string(errOut.Bytes())), err
}

func productionOnboardGitArgs(root string) ([]string, error) {
	if strings.Contains(root, ",") {
		return nil, verbs.Domainf("workspace root %q cannot be represented by Docker's bind-mount grammar", root)
	}
	args := []string{
		"run", "--rm", "--network", "none", "--read-only",
		"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--env", "HOME=/tmp", "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=16m",
	}
	if root != "" {
		args = append(args,
			"--mount", "type=bind,src="+root+",dst=/workspace",
			"--workdir", "/workspace")
	}
	image, err := configuredProductionImage()
	if err != nil {
		return nil, err
	}
	args = append(args, "--entrypoint", "git", image)
	return args, nil
}

func runProductionOnboardGitMutation(root string, gitArgs ...string) error {
	args, err := productionOnboardGitArgs(root)
	if err != nil {
		return err
	}
	args = append(args, gitArgs...)
	_, diagnostic, err := productionOnboardGitDockerRun(args)
	if err != nil {
		return verbs.Domainf("prepare workspace Git contract in mc-prod: %v: %s",
			err, diagnostic)
	}
	return nil
}

func verifyProductionOnboardGitVersion() error {
	args, err := productionOnboardGitArgs("")
	if err != nil {
		return err
	}
	stdout, diagnostic, err := productionOnboardGitDockerRun(append(args, "--version"))
	if err != nil {
		return verbs.Domainf("probe mc-prod Git version: %v: %s", err, diagnostic)
	}
	const prefix = "git version "
	version := strings.TrimSpace(strings.TrimPrefix(stdout, prefix))
	if !strings.HasPrefix(stdout, prefix) {
		return verbs.Domainf("mc-prod returned an unrecognized Git version %q", stdout)
	}
	parts := strings.Split(version, ".")
	if len(parts) < 2 {
		return verbs.Domainf("mc-prod returned an unrecognized Git version %q", stdout)
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	if majorErr != nil || minorErr != nil || major < 0 || minor < 0 {
		return verbs.Domainf("mc-prod returned an unrecognized Git version %q", stdout)
	}
	if major < 2 || (major == 2 && minor < 48) {
		return verbs.Domainf("mc-prod Git %s is below the required 2.48 floor for relative worktree metadata", version)
	}
	return nil
}

func brokerOnboardState(args []string, stdout, stderr io.Writer) int {
	a, err := parseOnboardArgs(args[1:])
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	var surfacesPlan verbs.OnboardSurfacesPlan
	if a.Section == "surfaces" {
		surfacesPlan, err = verbs.PrepareOnboardSurfacesHost(a)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	}
	var tunablesPlan verbs.OnboardTunablesPlan
	if a.Section == "tunables" {
		tunablesPlan, err = verbs.PrepareOnboardTunablesHost(a)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		a = tunablesPlan.Project(a)
	}
	req, err := verbs.PrepareOnboardState(a, releaseBuildID, gatewayControlVersion, configSchemaVersion)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	gitChanged, gitInitialized := false, false
	if req.Section == "project" && req.Project != "" {
		if err := verifyProductionOnboardGitVersion(); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		home, homeErr := configuredCanonicalHome()
		if homeErr != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("resolve Project Git posture home: %v", homeErr))
		}
		gitChanged, gitInitialized, err = verbs.PrepareOnboardProjectGit(
			req.WorkspaceRoot,
			verbs.OnboardGitPostureIntent{
				Home: home, DeploymentUUID: req.DeploymentUUID,
				Project: req.Project,
			}, productionOnboardGitMutation)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		homeChanged, homeErr := verbs.PrepareOnboardProjectToolHome(verbs.OnboardArgs{
			Project: req.Project, WorkspaceRoot: req.WorkspaceRoot,
			ProjectGitAuthorName:  req.ProjectGitAuthorName,
			ProjectGitAuthorEmail: req.ProjectGitAuthorEmail,
			ProjectSandboxProfile: req.ProjectSandboxProfile,
		}, os.Getuid())
		if homeErr != nil {
			return writeVerbError(stdout, stderr, homeErr)
		}
		gitChanged = gitChanged || homeChanged
	}
	helper, err := ensureRuntimeHelper()
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("ensure warm helper: %v", err))
	}
	if err := verbs.RecheckOnboardStateHost(req); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	surfaceHostStatus, surfaceHostDetail := "", ""
	if a.Section == "surfaces" {
		surfaceHostStatus, surfaceHostDetail, err = verbs.ApplyOnboardSurfacesHost(surfacesPlan)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	}
	tunablesConfigChanged := false
	if a.Section == "tunables" {
		tunablesConfigChanged, err = verbs.ApplyOnboardTunablesHost(tunablesPlan)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	}
	if a.Section == "surfaces" {
		if err := verbs.ProjectCurrentOnboardAlertRoutes(&req); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	}
	frame, err := json.Marshal(req)
	if err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("encode private onboard-state frame: %v", err))
	}
	privateOut := boundedDoctorBuffer{limit: privateOnboardStateOutputLimit}
	privateErr := boundedDoctorBuffer{limit: privateDoctorOutputLimit}
	cmd := exec.Command("docker", "exec", "-i", helper, "mc", "__onboard-state")
	cmd.Stdin = bytes.NewReader(frame)
	cmd.Stdout = &privateOut
	cmd.Stderr = &privateErr
	if err := cmd.Run(); err != nil {
		if privateErr.Len() > 0 {
			_, _ = stderr.Write(privateErr.Bytes())
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) && !privateOut.overflow {
			_, _ = stdout.Write(privateOut.Bytes())
			return exit.ExitCode()
		}
		return writeVerbError(stdout, stderr, verbs.Usagef("private onboard-state crossing failed: %v", err))
	}
	if privateOut.overflow || privateErr.overflow {
		return writeVerbError(stdout, stderr, verbs.Usagef("private onboard-state response exceeded its bound"))
	}
	var result verbs.OnboardStateResult
	dec := json.NewDecoder(bytes.NewReader(privateOut.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&result); err != nil {
		return writeVerbError(stdout, stderr, verbs.Usagef("decode private onboard-state response: %v", err))
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return writeVerbError(stdout, stderr, verbs.Usagef("private onboard-state response carries trailing data"))
	}
	if req.Section == "project" && req.Project == "" {
		if err := verbs.ValidateOnboardWorkspaceRoots(result.WorkspaceRoots); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		for _, root := range result.WorkspaceRoots {
			if err := verifyProductionOnboardGitVersion(); err != nil {
				return writeVerbError(stdout, stderr, err)
			}
			changed, err := verbs.RepairOnboardProjectGit(
				root.Root, productionOnboardGitMutation)
			if err != nil {
				return writeVerbError(stdout, stderr, err)
			}
			gitChanged = gitChanged || changed
		}
	}
	if gitChanged {
		result.Status = "done"
	}
	status, detail, err := verbs.FinalizeOnboardState(req, result)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if req.Section == "project" && req.Project != "" {
		detail = verbs.AppendProjectGitDetail(detail, req.WorkspaceRoot, gitInitialized)
	}
	if a.Section == "routing" {
		status, detail, err = verbs.OnboardRoutingHost(a)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	}
	if a.Section == "surfaces" {
		if surfaceHostStatus == "done" {
			status = "done"
		}
		detail += "; " + surfaceHostDetail
	}
	if a.Section == "tunables" {
		if tunablesConfigChanged {
			status = "done"
		}
		detail += "; config.toml tunable and Git policy verified"
	}
	// The filesystem half of the project section, deliberately HOST-side.
	// The helper authorized these roots as durable schema data and never
	// touched a path; the layout they name is built here, as the operator,
	// because every consumer proves it against the operator's uid. Gated on the
	// section by name: `supervision` also carries WorkspaceRoots through the
	// same result, and building the layout there would fire it twice.
	if a.Section == "project" || a.Section == "smoke" {
		status, detail, err = verbs.FinishOnboardProjectLayout(status, detail, result.WorkspaceRoots)
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	}
	if a.Section == "supervision" {
		home, err := configuredCanonicalHome()
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("resolve supervision MC_HOME: %v", err))
		}
		mcPath, err := os.Executable()
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("resolve installed mc binary: %v", err))
		}
		bunRecord, err := publishedSupervisionRecord(home)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("resolve the release record for native supervision: %v", err))
		}
		bunPath, err := hostBunPath(home, bunRecord)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("resolve bun for native supervision: %v", err))
		}
		dockerPath, err := exec.LookPath("docker")
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("resolve docker for native supervision: %v", err))
		}
		for target, value := range map[string]*string{"mc": &mcPath, "bun": &bunPath, "docker": &dockerPath} {
			if !filepath.IsAbs(*value) {
				absolute, absErr := filepath.Abs(*value)
				if absErr != nil {
					return writeVerbError(stdout, stderr, verbs.Usagef("resolve %s executable path: %v", target, absErr))
				}
				*value = absolute
			}
		}
		names, err := deployment.RuntimeNames(home)
		if err != nil {
			return writeVerbError(stdout, stderr, verbs.Usagef("derive supervision runtime names: %v", err))
		}
		image, err := configuredProductionImage()
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		projectImage, err := configuredProjectImage()
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		roots := make([]deployment.WorkspaceRoot, len(result.WorkspaceRoots))
		for i, root := range result.WorkspaceRoots {
			roots[i] = deployment.WorkspaceRoot{ID: root.ID, Root: root.Root}
		}
		hostConfig, err := verbs.LoadOnboardHostConfig()
		if err != nil {
			return writeVerbError(stdout, stderr, err)
		}
		prepareStatus := "ok"
		prepared := deployment.SupervisionResultFor(home, names)
		skipPrepare := false
		if a.ActivateSupervision {
			stopped, err := deployment.ReadServiceStops(home)
			if err != nil {
				return writeVerbError(stdout, stderr, err)
			}
			expectedUnits := 0
			for _, unit := range deployment.SupervisedUnits {
				if !stopped[unit] {
					expectedUnits++
				}
			}
			loadedUnits := 0
			for _, label := range prepared.Labels() {
				loaded, err := launchdLabelLoaded(label)
				if err != nil {
					return writeVerbError(stdout, stderr, verbs.Usagef("inspect existing native supervision: %s: %v", label, err))
				}
				if loaded {
					loadedUnits++
				}
			}
			// A partial load, such as a deployment from before the AgentsView
			// unit, is drift that only a reconfiguration repairs.
			skipPrepare = loadedUnits > 0
			if skipPrepare {
				matches := loadedUnits == expectedUnits
				if matches {
					var matchErr error
					matches, matchErr = deployment.SupervisionPolicyMatches(
						home, hostConfig.SupervisionPolicy)
					if matchErr != nil {
						return writeVerbError(stdout, stderr, verbs.Domainf(
							"validate loaded supervision policy: %v", matchErr))
					}
				}
				// A new release is drift too. Activation refuses a prepared
				// resident config from another release, so without this the
				// units stay loaded, the rewrite is skipped as "no change",
				// and activation then fails on the stale id — with
				// --reconfigure powerless, because it is only ever reached
				// through this comparison.
				if matches {
					releaseCurrent, releaseErr := deployment.SupervisionReleaseMatches(home, releaseBuildID)
					if releaseErr != nil {
						return writeVerbError(stdout, stderr, verbs.Domainf(
							"validate loaded supervision release: %v", releaseErr))
					}
					matches = releaseCurrent
				}
				if !matches && !a.ReconfigureSupervision {
					return writeVerbError(stdout, stderr, verbs.Domainf(
						"loaded supervision differs from config.toml or from this release; re-run with --reconfigure to stop the loaded units and apply it (operator-present), because restarting a running deployment is not a repair this section may choose on its own"))
				}
				if !matches {
					userHome, err := os.UserHomeDir()
					if err != nil {
						return writeVerbError(stdout, stderr, verbs.Usagef("resolve operator home for LaunchAgents: %v", err))
					}
					reconfigureStatus, reconfigured, err := deployment.ReconfigureSupervision(
						home, filepath.Join(userHome, "Library", "LaunchAgents"), releaseBuildID,
						deployment.SupervisionSpec{
							ReleaseBuildID: releaseBuildID, MCPath: mcPath, BunPath: bunPath,
							DockerPath: dockerPath, Image: image, ProjectImage: projectImage, SpineVolume: names.Volume,
							WorkspaceRoots:    roots,
							SupervisionPolicy: hostConfig.SupervisionPolicy,
						},
						deployment.SupervisionActivationDeps{
							Launchd: launchdController{}, Now: time.Now, Sleep: time.Sleep,
						},
					)
					if err != nil {
						return writeVerbError(stdout, stderr, verbs.Domainf("reconfigure native supervision: %v", err))
					}
					return writeOnboardSection(stdout, stderr, a.Section, reconfigureStatus, fmt.Sprintf(
						"%s, %s, and %s stopped, rewritten from config.toml, and reloaded; observed a release-bound resident tick",
						reconfigured.ResidentLabel, reconfigured.DashboardLabel, reconfigured.AgentsViewLabel))
				}
			}
		}
		if !skipPrepare {
			prepareStatus, prepared, err = deployment.PrepareSupervision(home, deployment.SupervisionSpec{
				ReleaseBuildID: releaseBuildID, MCPath: mcPath, BunPath: bunPath,
				DockerPath: dockerPath, Image: image, ProjectImage: projectImage, SpineVolume: names.Volume,
				WorkspaceRoots:    roots,
				SupervisionPolicy: hostConfig.SupervisionPolicy,
			}, launchdLabelLoaded)
			if err != nil {
				return writeVerbError(stdout, stderr, verbs.Domainf("prepare native supervision: %v", err))
			}
		}
		status = prepareStatus
		detail = fmt.Sprintf("resident, dashboard, and AgentsView configs and per-user LaunchAgents %s at %s; %s, %s, and %s verified unloaded",
			prepareStatus, prepared.Root, prepared.ResidentLabel, prepared.DashboardLabel, prepared.AgentsViewLabel)
		if a.ActivateSupervision {
			userHome, err := os.UserHomeDir()
			if err != nil {
				return writeVerbError(stdout, stderr, verbs.Usagef("resolve operator home for LaunchAgents: %v", err))
			}
			activationStatus, active, err := deployment.ActivateSupervision(
				home, filepath.Join(userHome, "Library", "LaunchAgents"), releaseBuildID,
				deployment.SupervisionActivationDeps{
					Launchd: launchdController{}, Now: time.Now, Sleep: time.Sleep,
				},
			)
			if err != nil {
				return writeVerbError(stdout, stderr, verbs.Domainf("activate native supervision: %v", err))
			}
			status = activationStatus
			detail = fmt.Sprintf("%s, %s, and %s loaded as per-user LaunchAgents; observed a release-bound resident tick",
				active.ResidentLabel, active.DashboardLabel, active.AgentsViewLabel)
		}
	}
	return writeOnboardSection(stdout, stderr, a.Section, status, detail)
}

type launchdController struct{}

func (launchdController) Loaded(label string) (bool, error) { return launchdLabelLoaded(label) }

func (launchdController) Bootstrap(plist string) error {
	target := fmt.Sprintf("gui/%d", os.Getuid())
	label := strings.TrimSuffix(filepath.Base(plist), ".plist")
	if out, err := exec.Command("/bin/launchctl", "enable", target+"/"+label).CombinedOutput(); err != nil {
		return fmt.Errorf("enable %s: %v: %s", label, err, strings.TrimSpace(string(out)))
	}
	out, err := exec.Command("launchctl", "bootstrap", target, plist).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootstrap %s %s: %v: %s", target, plist, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (launchdController) Bootout(label string) error {
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	out, err := exec.Command("launchctl", "bootout", target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl bootout %s: %v: %s", target, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func launchdLabelLoaded(label string) (bool, error) {
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	out, err := exec.Command("launchctl", "print", target).CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		text := strings.ToLower(string(out))
		if strings.Contains(text, "could not find service") || strings.Contains(text, "service not found") {
			return false, nil
		}
	}
	return false, fmt.Errorf("launchctl print %s: %v: %s", target, err, strings.TrimSpace(string(out)))
}
