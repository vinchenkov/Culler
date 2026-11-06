//go:build !test_fake_routing

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"mc/deployment"
	"mc/verbs"
)

type uninstallReport struct {
	DryRun         bool                     `json:"dry_run"`
	FullWipe       bool                     `json:"full_wipe"`
	Containers     []string                 `json:"containers"`
	RuntimeVolumes []string                 `json:"runtime_volumes"`
	CacheVolumes   []string                 `json:"cache_volumes"`
	Volumes        []string                 `json:"volumes"`
	Images         []string                 `json:"images"`
	LaunchAgents   []string                 `json:"launch_agents"`
	Homes          []string                 `json:"homes"`
	SmokeRoots     []string                 `json:"smoke_roots"`
	Software       []string                 `json:"software"`
	HostBuns       []string                 `json:"host_buns"`
	Diagnostics    []string                 `json:"diagnostics"`
	Workspaces     []verbs.WorkspaceCleanup `json:"workspaces"`
	Binaries       []string                 `json:"binaries"`
	Commands       []string                 `json:"commands,omitempty"`
	Shells         []string                 `json:"shells,omitempty"`
	KeptHomes      []string                 `json:"kept_homes"`
	KeptVolumes    []string                 `json:"kept_volumes"`
	Problems       []string                 `json:"problems"`
	Uninstalled    bool                     `json:"uninstalled"`
}

const uninstallWorkspaceManifest = "uninstall-workspaces.json"

var uninstallHostSecrets = deployment.DeleteInstallationHostSecrets

type uninstallPresentation struct {
	plan     func(uninstallReport)
	problems func([]string)
	progress func(string)
	confirm  func(uninstallReport) bool
}

func brokerUninstall(args []string, stdout, stderr io.Writer) int {
	return runUninstall(args, stdout, stderr, []string{"/tmp", os.TempDir()})
}

func runUninstall(args []string, stdout, stderr io.Writer, tempRoots []string) int {
	return runUninstallWith(context.Background(), args, stdout, stderr, tempRoots, uninstallPresentation{
		plan:     func(report uninstallReport) { renderUninstallPlan(stderr, report) },
		problems: func(problems []string) { renderUninstallProblems(stderr, problems) },
		progress: func(message string) { fmt.Fprintln(stderr, message) },
	})
}

func runUninstallWith(ctx context.Context, args []string, stdout, stderr io.Writer, tempRoots []string, ui uninstallPresentation) int {
	fs := newFlags("mc __uninstall")
	confirm := fs.Bool("confirm", false, "confirm removal")
	dryRun := fs.Bool("dry-run", false, "show the plan without removing anything")
	fullWipe := fs.Bool("full-wipe", false, "remove software and Culler data (default)")
	keepData := fs.Bool("keep-data", false, "remove software and preserve data")
	var extraWorkspaces, extraHomes, extraBinaries repeatedFlag
	fs.Var(&extraWorkspaces, "workspace", "additional Project path (repeatable)")
	fs.Var(&extraHomes, "home", "additional deployment home (repeatable)")
	fs.Var(&extraBinaries, "binary", "installed program path (repeatable)")
	if err := parse(fs, args[1:]); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if *fullWipe && *keepData {
		return writeVerbError(stdout, stderr, verbs.Usagef("choose --keep-data or --full-wipe"))
	}
	*fullWipe = !*keepData
	if !*confirm && !*dryRun {
		return writeVerbError(stdout, stderr, verbs.Domainf("use ./uninstall.sh --dry-run to review the removal plan"))
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(id, "mc __uninstall"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	report := uninstallReport{DryRun: *dryRun, FullWipe: *fullWipe, HostBuns: []string{}, Problems: []string{}}
	problem := func(format string, a ...any) { report.Problems = append(report.Problems, fmt.Sprintf(format, a...)) }
	finish := func() int {
		if len(report.Problems) > 0 {
			ui.problems(report.Problems)
		}
		report.Uninstalled = !report.DryRun && len(report.Problems) == 0
		if err := writeJSON(stdout, report); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		if len(report.Problems) > 0 {
			return 1
		}
		return 0
	}
	docker := execDockerRunner{}
	labels, plists, plistHomes, err := uninstallLaunchAgents()
	report.LaunchAgents = labels
	if err != nil {
		problem("inspect startup units: %v", err)
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		problem("find user home: %v", err)
		return finish()
	}
	configured, err := configuredCanonicalHome()
	if err != nil {
		problem("resolve deployment home: %v", err)
	}
	candidates := unionNames([]string{configured, filepath.Join(userHome, ".culler"), filepath.Join(userHome, ".mission-control")}, plistHomes, extraHomes)
	var homes []string
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		home, err := uninstallHome(candidate, userHome)
		if err != nil {
			problem("deployment directory %s: %v", candidate, err)
			continue
		}
		homes = unionNames(homes, []string{home})
	}
	var locks []*os.File
	defer func() {
		for _, lock := range locks {
			_ = lock.Close()
		}
	}()
	if !*dryRun && len(report.Problems) == 0 {
		lockedHomes := map[string]bool{}
		for _, home := range homes {
			if _, err := os.Stat(home); os.IsNotExist(err) {
				continue
			}
			lock, err := lockPublishedSetup(home)
			if err != nil {
				problem("lock the deployment %s: %v", home, err)
				return finish()
			}
			locks = append(locks, lock)
			lockedHomes[home] = true
		}
		labels, plists, plistHomes, err = uninstallLaunchAgents()
		report.LaunchAgents = labels
		if err != nil {
			problem("inspect startup units after the deployment locks: %v", err)
			return finish()
		}
		for _, path := range unionNames(homes, plistHomes) {
			home, err := deployment.CanonicalHome(path)
			if err != nil {
				problem("resolve the deployment directory %s: %v", path, err)
				continue
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) && !lockedHomes[home] {
				problem("The installation changed during the preview. Retry culler uninstall.")
			}
		}
		if len(report.Problems) > 0 {
			return finish()
		}
	}
	report.Containers = unionNames(
		dockerNames(docker, problem, "ps", "-a", "--filter", "label=mc-managed=true", "--format", "{{.Names}}"),
		verbs.CullerContainers(dockerNames(docker, problem, "ps", "-a", "--format", "{{.Names}}")),
	)
	volumeNames := dockerNames(docker, problem, "volume", "ls", "--format", "{{.Name}}")
	volumes := verbs.CullerVolumes(volumeNames)
	report.Images = uninstallImageRefs(docker, problem)
	if *fullWipe {
		report.CacheVolumes = verbs.ProjectCacheVolumes(volumeNames, homes)
		report.Images = unionNames(report.Images, uninstallProjectImageRefs(docker, homes, problem))
	}
	for _, suffix := range unlocatedDeployments(uninstallReport{Volumes: volumes, Containers: report.Containers, LaunchAgents: report.LaunchAgents}, homes) {
		problem("deployment %s has no known directory; add --home <path> to ./uninstall.sh", suffix)
	}

	// Keep a project inventory so a later full wipe works after the helpers are gone.
	workspaces := append([]string{}, extraWorkspaces...)
	for _, home := range homes {
		names, _ := deployment.RuntimeNames(home)
		roots, readErr := uninstallWorkspaceRoots(docker, names.Helper)
		if readErr != nil {
			body, manifestErr := os.ReadFile(filepath.Join(home, uninstallWorkspaceManifest))
			if manifestErr == nil {
				manifestErr = json.Unmarshal(body, &roots)
			}
			hasVolume := containsName(volumes, names.Volume)
			if manifestErr != nil && hasVolume && len(extraWorkspaces) == 0 {
				problem("Project paths for %s are unavailable; add every Project with --workspace <path> to ./uninstall.sh", home)
			}
		}
		workspaces = unionNames(workspaces, roots)
	}
	for _, workspace := range workspaces {
		for _, home := range homes {
			if withinUninstallPath(home, workspace) {
				problem("deployment directory %s contains Project %s; refusing removal", home, workspace)
			}
		}
		if *fullWipe {
			cleanup, err := verbs.CleanWorkspace(workspace, true)
			if err != nil {
				problem("inspect Project %s: %v", workspace, err)
				continue
			}
			report.Workspaces = append(report.Workspaces, cleanup)
			for _, retained := range cleanup.Retained {
				problem("Project cleanup needs attention: %s", retained)
			}
		}
	}
	for _, home := range homes {
		// Both modes remove the host Bun with the other Culler software.
		report.HostBuns = appendExisting(report.HostBuns, filepath.Join(home, "bun"), problem)
		if *fullWipe {
			report.Homes = appendExisting(report.Homes, home, problem)
			smoke, _ := deployment.SmokeWorkspaceBase(home)
			registeredProject := false
			for _, workspace := range workspaces {
				if withinUninstallPath(smoke, workspace) {
					if _, err := os.Lstat(workspace); !os.IsNotExist(err) {
						registeredProject = true
					}
				}
			}
			if !registeredProject {
				report.SmokeRoots = appendExisting(report.SmokeRoots, smoke, problem)
			}
		} else {
			report.KeptHomes = appendExisting(report.KeptHomes, home, problem)
			for _, name := range []string{"release", "bin", "supervision/LaunchAgents", "resident.socket"} {
				target := filepath.Join(home, name)
				if !withinUninstallPath(home, filepath.Dir(target)) {
					problem("installed path escapes deployment directory: %s", target)
					continue
				}
				report.Software = appendExisting(report.Software, target, problem)
			}
		}
	}
	var dataVolumes []string
	for _, volume := range volumes {
		if strings.HasPrefix(volume, "mc-runtime-") {
			report.RuntimeVolumes = append(report.RuntimeVolumes, volume)
		} else {
			dataVolumes = append(dataVolumes, volume)
		}
	}
	if *fullWipe {
		report.Volumes = dataVolumes
		report.Diagnostics = uninstallDiagnostics(tempRoots)
	} else {
		report.KeptVolumes = dataVolumes
	}
	binaryCandidates := unionNames(extraBinaries, []string{filepath.Join(userHome, ".local", "bin", "mc")})
	for _, home := range homes {
		body, err := os.ReadFile(filepath.Join(home, "supervision", "resident.json"))
		if err == nil {
			var config struct {
				MCPath string `json:"mcPath"`
			}
			if err := json.Unmarshal(body, &config); err != nil {
				problem("read program path in %s: %v", home, err)
			} else if config.MCPath != "" {
				binaryCandidates = unionNames(binaryCandidates, []string{config.MCPath})
			}
		}
	}
	for _, binary := range binaryCandidates {
		if !filepath.IsAbs(binary) || filepath.Base(binary) != "mc" {
			problem("invalid installed program path %s", binary)
			continue
		}
		if info, err := os.Lstat(binary); err == nil && info.IsDir() {
			problem("program path is a directory: %s", binary)
			continue
		}
		report.Binaries = appendExisting(report.Binaries, binary, problem)
	}
	var removeCommands []func() error
	for _, home := range homes {
		command, err := deployment.ReadCLIInstallation(home)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			problem("inspect the Culler command for %s: %v", home, err)
			continue
		}
		for _, file := range append(command.Previous, deployment.CLIExecutable{Path: command.Path, SHA256: command.SHA256}) {
			if digest, err := deployment.CLIFileDigest(file.Path); err == nil && digest == file.SHA256 {
				report.Commands = unionNames(report.Commands, []string{file.Path})
			}
		}
		for _, edit := range command.Shells {
			report.Shells = unionNames(report.Shells, []string{edit.Path})
		}
		remove, err := deployment.PrepareCLIUninstall(home)
		if err != nil {
			problem("prepare removal of the Culler command: %v", err)
		} else {
			removeCommands = append(removeCommands, remove)
		}
	}
	ui.plan(report)
	if *dryRun || len(report.Problems) > 0 {
		return finish()
	}
	if ctx.Err() != nil || (ui.confirm != nil && !ui.confirm(report)) {
		return 130
	}
	progress := func(message string) bool {
		if ctx.Err() != nil {
			problem("The uninstall stopped after cancellation. Some resources can remain.")
			return false
		}
		ui.progress(message)
		return true
	}

	if !progress("Stopping Culler services...") {
		return finish()
	}
	for _, label := range report.LaunchAgents {
		if err := launchctlBootout(label); err != nil {
			problem("stop %s: %v", label, err)
		}
	}
	if len(report.Problems) > 0 {
		return finish()
	}
	for _, home := range homes {
		if _, err := os.Stat(home); os.IsNotExist(err) {
			continue
		}
		body, err := json.Marshal(workspaces)
		if err == nil {
			err = os.WriteFile(filepath.Join(home, uninstallWorkspaceManifest), body, 0o600)
		}
		if err != nil {
			problem("save Project paths in %s: %v", home, err)
		}
	}
	if len(report.Problems) > 0 {
		return finish()
	}
	for _, plist := range plists {
		if err := os.Remove(plist); err != nil && !os.IsNotExist(err) {
			problem("remove %s: %v", plist, err)
		}
	}
	if !progress("Removing containers and images...") {
		return finish()
	}
	for _, name := range report.Containers {
		if out, err := docker.CombinedOutput("container", "rm", "-f", name); err != nil {
			problem("remove container %s: %v: %s", name, err, strings.TrimSpace(string(out)))
		}
	}
	// A failed stop/removal must never be followed by deleting its data.
	if len(report.Problems) > 0 {
		return finish()
	}
	for _, name := range report.CacheVolumes {
		if out, err := docker.CombinedOutput("volume", "rm", name); err != nil {
			problem("remove package cache %s: %v: %s", name, err, strings.TrimSpace(string(out)))
		}
	}
	for _, ref := range report.Images {
		if out, err := docker.CombinedOutput("image", "rm", ref); err != nil {
			problem("remove image %s: %v: %s", ref, err, strings.TrimSpace(string(out)))
		}
	}
	if len(report.Problems) > 0 {
		return finish()
	}
	if *fullWipe {
		if !progress("Removing Culler data...") {
			return finish()
		}
		for _, workspace := range report.Workspaces {
			if _, err := verbs.CleanWorkspace(workspace.Workspace, false); err != nil {
				problem("clean Project %s: %v", workspace.Workspace, err)
			}
		}
		if len(report.Problems) > 0 {
			return finish()
		}
		for _, name := range report.Volumes {
			if out, err := docker.CombinedOutput("volume", "rm", name); err != nil {
				problem("remove volume %s: %v: %s", name, err, strings.TrimSpace(string(out)))
			}
		}
		if len(report.Problems) > 0 {
			return finish()
		}
	}
	for _, name := range report.RuntimeVolumes {
		if out, err := docker.CombinedOutput("volume", "rm", name); err != nil {
			problem("remove runtime volume %s: %v: %s", name, err, strings.TrimSpace(string(out)))
		}
	}
	if len(report.Problems) > 0 {
		return finish()
	}
	if *fullWipe {
		for _, home := range report.Homes {
			if err := uninstallHostSecrets(home); err != nil {
				problem("remove credentials for %s: %v", home, err)
			}
		}
		if len(report.Problems) > 0 {
			return finish()
		}
	}
	if !progress("Removing installed files...") {
		return finish()
	}
	for _, root := range unionNames(report.Software, report.HostBuns, report.SmokeRoots, report.Homes, report.Diagnostics) {
		if err := verbs.RemoveTree(root); err != nil {
			problem("remove %s: %v", root, err)
		}
	}
	// Preserve the installed program after a failure so uninstall can be retried.
	if len(report.Problems) > 0 {
		return finish()
	}
	for _, binary := range report.Binaries {
		if err := os.Remove(binary); err != nil && !os.IsNotExist(err) {
			problem("remove %s: %v", binary, err)
		}
	}
	if len(report.Problems) > 0 {
		return finish()
	}
	for _, remove := range removeCommands {
		if err := remove(); err != nil {
			problem("remove the Culler command: %v", err)
		}
	}
	return finish()
}

func containsName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
}

func withinUninstallPath(parent, child string) bool {
	for _, path := range []*string{&parent, &child} {
		if resolved, err := filepath.EvalSymlinks(*path); err == nil {
			*path = resolved
		}
	}
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func uninstallHome(path, userHome string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("expected an absolute path")
	}
	path = filepath.Clean(path)
	resolved, err := deployment.CanonicalHome(path)
	if err != nil {
		return "", err
	}
	userHome, err = deployment.CanonicalHome(userHome)
	if err != nil {
		return "", err
	}
	if withinUninstallPath(resolved, userHome) {
		return "", fmt.Errorf("refusing a user home or its ancestor")
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return resolved, nil
	}
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("expected a plain deployment directory")
	}
	if _, err := os.Lstat(filepath.Join(path, ".git")); !os.IsNotExist(err) {
		return "", fmt.Errorf("refusing a repository")
	}
	for _, marker := range []string{"config.toml", "deployment.uuid"} {
		if info, err := os.Lstat(filepath.Join(path, marker)); err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("no deployment %s; refusing an unrecognized directory", marker)
		}
	}
	return resolved, nil
}

func appendExisting(paths []string, path string, problem func(string, ...any)) []string {
	if _, err := os.Lstat(path); err == nil {
		return append(paths, path)
	} else if !os.IsNotExist(err) {
		problem("inspect %s: %v", path, err)
	}
	return paths
}

func renderUninstallPlan(w io.Writer, report uninstallReport) {
	section := func(title string, items []string) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(w, "  %s (%d)\n", title, len(items))
		for _, item := range items {
			fmt.Fprintf(w, "    %s\n", item)
		}
	}
	if report.FullWipe {
		fmt.Fprintln(w, "Full wipe: software and Culler data.", "\nProject repositories stay.")
	} else {
		fmt.Fprintln(w, "Uninstall software; keep Culler data.")
	}
	fmt.Fprintln(w, "\nRemoval plan:")
	section("Deployment directories", report.Homes)
	section("Smoke workspaces", report.SmokeRoots)
	section("Setup diagnostics", report.Diagnostics)
	section("Installed files", report.Software)
	section("Culler Bun", report.HostBuns)
	section("Database volumes", report.Volumes)
	section("Culler runtime volumes", report.RuntimeVolumes)
	section("Package cache volumes", report.CacheVolumes)
	section("Containers", report.Containers)
	section("Images", report.Images)
	section("Startup units", report.LaunchAgents)
	section("Programs", report.Binaries)
	section("Culler commands", report.Commands)
	section("Culler entries in shell files", report.Shells)
	for _, workspace := range report.Workspaces {
		removed := append([]string{}, workspace.Removed...)
		for _, branch := range workspace.Branches {
			removed = append(removed, "branch "+branch)
		}
		section("In Project "+workspace.Workspace, removed)
		section("Kept in "+workspace.Workspace, workspace.Retained)
	}
	if !report.FullWipe {
		fmt.Fprintln(w, "\nData kept for reinstallation:")
		section("Configuration, history and credentials", report.KeptHomes)
		section("Database volumes", report.KeptVolumes)
		fmt.Fprintln(w, "  Project files stay.\n  Reinstall at the same deployment path.")
	}
}

func renderUninstallProblems(w io.Writer, problems []string) {
	fmt.Fprintln(w, "\nUninstall incomplete:")
	for _, problem := range problems {
		fmt.Fprintf(w, "  %s\n", problem)
	}
	fmt.Fprintln(w, "Fix the listed issues, then retry\n./uninstall.sh with the same options.")
}

func uninstallWorkspaceRoots(docker dockerCommandRunner, helper string) ([]string, error) {
	out, err := docker.CombinedOutput("exec", helper, "mc", "__uninstall-workspace-roots")
	if err != nil {
		return nil, err
	}
	var payload struct {
		WorkspaceRoots []string `json:"workspace_roots"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, err
	}
	return payload.WorkspaceRoots, nil
}

// Inspect both on-disk units and loaded units whose plist was removed manually.
func uninstallLaunchAgents() (labels, plists, homes []string, err error) {
	if runtime.GOOS != "darwin" {
		return nil, nil, nil, nil
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, nil, err
	}
	dir := filepath.Join(userHome, "Library", "LaunchAgents")
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, nil, err
	}
	var files []string
	for _, entry := range entries {
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	labels, plists = verbs.CullerLaunchAgents(files)
	for _, plist := range plists {
		body, err := exec.Command("plutil", "-convert", "json", "-o", "-", plist).Output()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("read %s: %w", plist, err)
		}
		var config struct{ EnvironmentVariables map[string]string }
		if err := json.Unmarshal(body, &config); err != nil {
			return nil, nil, nil, err
		}
		if home := config.EnvironmentVariables["MC_HOME"]; home != "" {
			homes = unionNames(homes, []string{home})
		} else if home, ok := deployment.AgentsViewHomeForData(config.EnvironmentVariables["AGENTSVIEW_DATA_DIR"]); ok {
			homes = unionNames(homes, []string{home})
		}
	}
	out, err := exec.Command("launchctl", "list").Output()
	if err != nil {
		return nil, nil, nil, err
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && verbs.CullerLaunchLabel(fields[2]) {
			labels = unionNames(labels, []string{fields[2]})
		}
	}
	return labels, plists, homes, nil
}

func launchctlBootout(label string) error {
	loaded, err := launchdLabelLoaded(label)
	if err != nil || !loaded {
		return err
	}
	if err := (launchdController{}).Bootout(label); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := launchdLabelLoaded(label)
		if err != nil || !loaded {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("service is still running after bootout")
}

func unlocatedDeployments(report uninstallReport, homes []string) []string {
	known := map[string]bool{}
	for _, home := range homes {
		if names, err := deployment.RuntimeNames(home); err == nil {
			known[names.Suffix] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	note := func(suffix string) {
		if suffix == "" || known[suffix] || seen[suffix] {
			return
		}
		seen[suffix] = true
		out = append(out, suffix)
	}
	for _, name := range report.Volumes {
		note(strings.TrimPrefix(strings.TrimPrefix(name, "mc-spine-"), "mc-runtime-"))
	}
	for _, name := range report.Containers {
		if strings.HasPrefix(name, "mc-helper-") {
			note(strings.TrimPrefix(name, "mc-helper-"))
		}
	}
	for _, label := range report.LaunchAgents {
		parts := strings.Split(label, ".")
		if len(parts) >= 3 {
			note(parts[2])
		}
	}
	// Only the derived 12-hex suffixes name a deployment; e2e leftovers and
	// anything else are not deployments and have no home to look for.
	var deployments []string
	for _, suffix := range out {
		if len(suffix) == 12 {
			deployments = append(deployments, suffix)
		}
	}
	return deployments
}

// unionNames preserves the inventory order while removing duplicates.
func unionNames(lists ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range lists {
		for _, name := range list {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

func dockerNames(docker dockerCommandRunner, problem func(string, ...any), args ...string) []string {
	out, err := docker.CombinedOutput(args...)
	if err != nil {
		problem("docker %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		return nil
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// uninstallImageRefs returns only tags owned by Culler.
func uninstallImageRefs(docker dockerCommandRunner, problem func(string, ...any)) []string {
	var refs []string
	for _, repo := range verbs.UninstallImages {
		out, err := docker.CombinedOutput("image", "ls", "--format", "{{.Repository}}:{{.Tag}}", repo)
		if err != nil {
			problem("list image %s: %v: %s", repo, err, strings.TrimSpace(string(out)))
			continue
		}
		for _, line := range strings.Split(string(out), "\n") {
			if ref := strings.TrimSpace(line); strings.HasPrefix(ref, repo+":") {
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

func uninstallProjectImageRefs(docker dockerCommandRunner, homes []string, problem func(string, ...any)) []string {
	var selected []string
	refs := dockerNames(docker, problem, "image", "ls", "--filter", "label=com.culler.environment.operation", "--format", "{{.Repository}}:{{.Tag}}")
	for _, ref := range refs {
		if !verbs.ProjectImageReference(ref, homes) {
			continue
		}
		body, err := docker.CombinedOutput("image", "inspect", "--format", "{{json .Config.Labels}}", ref)
		if err != nil {
			problem("inspect Project image %s: %v", ref, err)
			continue
		}
		var labels map[string]string
		if json.Unmarshal(body, &labels) != nil || labels["com.culler.environment.operation"] != ref[strings.LastIndex(ref, ":")+1:] {
			problem("Project image %s has a different operation identity", ref)
			continue
		}
		selected = append(selected, ref)
	}
	return selected
}

// Diagnostic directories have no deployment home. Match their content and owner,
// not just their temporary name, and never walk into another user's directory.
func uninstallDiagnostics(tempRoots []string) []string {
	var found []string
	for _, root := range tempRoots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name(), "mc-setup-") || !entry.IsDir() {
				continue
			}
			suffix := strings.TrimPrefix(entry.Name(), "mc-setup-")
			if suffix == "" || strings.Trim(suffix, "0123456789") != "" {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			owner, ok := info.Sys().(*syscall.Stat_t)
			if !ok || int(owner.Uid) != os.Getuid() {
				continue
			}
			path := filepath.Join(root, entry.Name())
			files, err := os.ReadDir(path)
			if err != nil || len(files) != 1 || files[0].Name() != "diagnostic.json" || !files[0].Type().IsRegular() {
				continue
			}
			body, err := os.ReadFile(filepath.Join(path, "diagnostic.json"))
			if err != nil {
				continue
			}
			var record struct {
				Operation  string `json:"operation"`
				ObservedAt string `json:"observed_at"`
				Expected   struct {
					ProtocolVersion int    `json:"protocol_version"`
					SpineSchemaID   string `json:"spine_schema_id"`
				} `json:"expected_runtime_identity"`
			}
			if json.Unmarshal(body, &record) != nil || record.Operation == "" || record.Expected.ProtocolVersion != 1 || record.Expected.SpineSchemaID == "" {
				continue
			}
			if _, err := time.Parse(time.RFC3339Nano, record.ObservedAt); err != nil {
				continue
			}
			if real, err := filepath.EvalSymlinks(path); err == nil {
				found = unionNames(found, []string{real})
			}
		}
	}
	return found
}
