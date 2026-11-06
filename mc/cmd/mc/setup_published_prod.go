//go:build !test_fake_routing

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/distribution"
	"mc/onboarding"
	"mc/substrate"
	"mc/verbs"
)

type publishedSetup struct {
	update              *deployment.UpdateJournal
	stagedUpdate        *deployment.UpdateJournal
	resumingUpdate      bool
	candidateAssessment *updateCandidateAssessment
	home                string
	root                string
	record              distribution.Record
	installed           distribution.Record
	agentsView          deployment.AgentsViewPin
	lock                *os.File
	maintenanceID       string
	prepared            bool
	forceMaintenance    bool
	context             context.Context
	interrupted         *cullerOperation
	recoverOperation    func(*cullerOperation) error
	started             time.Time
	docker              func(context.Context, ...string) ([]byte, error)
	pull                func(context.Context, distribution.Record, func(string)) error
	states              func(string) (map[string]bool, error)
	restore             func(string, map[string]bool) error
	now                 func() time.Time
	sleep               func(time.Duration)
	images              *imageRemovalRecord
	hostBunProblem      string
}

func loadPublishedSetup(home string, args verbs.OnboardArgs) (*publishedSetup, error) {
	path, root := os.Getenv("CULLER_RELEASE_RECORD"), os.Getenv("CULLER_PACKAGE_ROOT")
	if path == "" && root == "" {
		return nil, nil
	}
	if !filepath.IsAbs(path) || !filepath.IsAbs(root) {
		return nil, errors.New("The published release paths are invalid. Download install.sh again.")
	}
	if !filepath.IsAbs(os.Getenv("MC_BIN_DIR")) {
		return nil, errors.New("The installer binary directory is invalid. Run install.sh again.")
	}
	r, err := distribution.Load(path)
	if err != nil {
		return nil, err
	}
	if r.Commit != releaseBuildID || r.Schema != substrate.CurrentSchemaID {
		return nil, errors.New("The application package differs from its release record. Download install.sh again.")
	}
	if args.HostReleaseSource != filepath.Join(root, "host") || args.ReleaseSource != filepath.Join(root, "runner") {
		return nil, errors.New("The application package paths differ from the selected release.")
	}
	if err := deployment.ValidateHostRelease(args.HostReleaseSource); err != nil {
		return nil, err
	}
	if err := deployment.ValidateRunnerRelease(args.ReleaseSource); err != nil {
		return nil, err
	}
	for _, name := range []string{"mc", "install.sh", "agentsview"} {
		info, err := os.Lstat(filepath.Join(root, "bin", name))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return nil, fmt.Errorf("The application package lacks executable %s. Download install.sh again.", name)
		}
	}
	if _, err := deployment.CLIFileDigest(filepath.Join(root, "bin", "mc")); err != nil {
		return nil, err
	}
	pin, err := deployment.LoadAgentsViewPin()
	if err != nil {
		return nil, err
	}
	if err := checkPublishedAgentsView(root, r, pin); err != nil {
		return nil, err
	}
	hostBun, err := deployment.LoadHostBunPin()
	if err != nil {
		return nil, err
	}
	if err := checkPublishedHostBun(home, r, hostBun); err != nil {
		return nil, err
	}
	lock, err := lockPublishedSetup(home)
	if err != nil {
		return nil, err
	}
	if operation, err := readCullerOperation(home); err != nil || operation != nil {
		_ = lock.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("An interrupted service command requires recovery. Repeat that service command before you run the installer.")
	}
	now := time.Now()
	setup := &publishedSetup{home: home, root: root, record: r, agentsView: pin, lock: lock, pull: pullPublishedImage,
		docker: func(ctx context.Context, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, publishedUpdateDockerProgram(home), args...).CombinedOutput()
		},
		states: setupServiceStates, restore: restorePublishedServices, started: publishedInstallStartedAt(os.Getenv("CULLER_INSTALL_STARTED_AT"), now), now: time.Now, sleep: time.Sleep}
	var prior struct {
		ID string `json:"id"`
	}
	if body, err := os.ReadFile(filepath.Join(home, "state", "install-maintenance.json")); err == nil && json.Unmarshal(body, &prior) == nil {
		setup.maintenanceID = prior.ID
	}
	journal, err := deployment.LoadUpdateJournal(home)
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	if journal != nil && journal.Phase != "complete" && journal.Phase != "restored" {
		if !samePublishedRecord(journal.Candidate, r) {
			_ = lock.Close()
			return nil, errors.New("An incomplete update requires its retained release. Run culler update.")
		}
		if err := deployment.RetainUpdateCandidate(home, journal, journal.CandidateRoot); err != nil {
			_ = lock.Close()
			return nil, err
		}
		setup.update, setup.maintenanceID, setup.started = journal, journal.ID, journal.StartedAt
		if journal.Previous != nil {
			setup.installed = *journal.Previous
		}
	}
	return setup, nil
}

func publishedInstallStartedAt(value string, now time.Time) time.Time {
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds <= 0 || seconds > now.Unix() {
		return now
	}
	return time.Unix(seconds, 0).UTC()
}

func lockPublishedSetup(home string) (*os.File, error) {
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(home, "install.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		file.Close()
		return nil, errors.New("The install lock requires an owner-only regular file.")
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, errors.New("Another installer or service command controls this deployment. Retry after it completes.")
	}
	return file, nil
}

func (p *publishedSetup) close() {
	if p.update == nil || !p.update.BlocksWork() {
		p.releaseMaintenance()
	}
	if p.stagedUpdate != nil {
		_ = os.RemoveAll(deployment.UpdateDirectory(p.home, p.stagedUpdate.ID))
	}
	if p.lock != nil {
		_ = p.lock.Close()
	}
}

var saveUpdateActivationJournal = deployment.SaveUpdateJournal

func (p *publishedSetup) releaseMaintenance() error {
	if p.maintenanceID == "" {
		return nil
	}
	path := filepath.Join(p.home, "state", "install-maintenance.json")
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var request struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &request) != nil {
		return errors.New("The maintenance request is unreadable. Run culler update to repair recovery.")
	}
	if request.ID != p.maintenanceID {
		return errors.New("Another operation owns the maintenance request. Keep Culler stopped and inspect its recovery record.")
	}
	journal, err := deployment.LoadUpdateJournal(p.home)
	if err != nil {
		return err
	}
	if p.update != nil && (journal == nil || journal.ID != p.update.ID || journal.Phase != p.update.Phase) {
		return errors.New("The update record changed. Run culler update again.")
	}
	if journal != nil && journal.ID == request.ID {
		if journal.BlocksWork() {
			return errors.New("The incomplete update must keep its maintenance request. Run culler update to recover.")
		}
		// A visible rename can precede a failed directory sync.
		if err := saveUpdateActivationJournal(p.home, journal); err != nil {
			return err
		}
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (p *publishedSetup) operationContext() context.Context {
	if p.context != nil {
		return p.context
	}
	return context.Background()
}

func (p *publishedSetup) cancelOperation() error {
	if p.update != nil {
		return errors.New("The update remains stopped. Run culler update to retry or restore it.")
	}
	if p.interrupted == nil {
		return nil
	}
	if p.recoverOperation != nil {
		return p.recoverOperation(p.interrupted)
	}
	r, err := readCullerRuntime(p.home)
	if err != nil {
		return err
	}
	manager, err := cullerManagerForRuntime(p.home, r, p.record)
	if err != nil {
		return err
	}
	manager.maintenance = p
	p.forceMaintenance = true
	return manager.restore(p.interrupted, func(string) {})
}

func (p *publishedSetup) configure(deps *setupDependencies) {
	deps.assessCandidate = func() ([]string, error) {
		if err := p.retainCandidate(); err != nil {
			return nil, err
		}
		if err := p.assessCandidate(p.operationContext()); err != nil {
			return nil, err
		}
		if p.candidateAssessment == nil {
			return nil, nil
		}
		return p.candidateAssessment.Lines(), nil
	}
	deps.finishExisting = func() error { return p.finishExisting() }
	deps.image = func(ctx context.Context, _ string, _ string, report func(string)) error {
		ctx, cancel := context.WithTimeout(p.operationContext(), 30*time.Minute)
		defer cancel()
		return publishedSetupFailure(p.prepare(ctx, report))
	}
	deps.rebuildImage = deps.image
	deps.beforeChange = func(_ string, _ verbs.OnboardArgs, report func(string)) error {
		ctx, cancel := context.WithTimeout(p.operationContext(), 30*time.Minute)
		defer cancel()
		return publishedSetupFailure(p.beforeUpdate(ctx, report))
	}
	deps.saveSource = func(home string, _ verbs.OnboardArgs) error { return p.stageInstalled() }
	deps.agentsView = func(string) error { return p.installAgentsView() }
	stop := deps.stop
	deps.stop = func(home string) error {
		if p.update != nil {
			return nil
		}
		ctx, cancel := context.WithTimeout(p.operationContext(), 30*time.Second)
		defer cancel()
		return p.stopAndRecheck(ctx, stop)
	}
	section := deps.section
	deps.section = func(name string, args verbs.OnboardArgs) error {
		if err := p.operationContext().Err(); err != nil {
			return errors.Join(onboarding.ErrExit, err)
		}
		if name == "supervision" && p.update != nil {
			if err := p.updatePhase("verify"); err != nil {
				return err
			}
		}
		err := section(name, args)
		if p.operationContext().Err() != nil {
			return errors.Join(onboarding.ErrExit, p.operationContext().Err())
		}
		return err
	}
	ready := deps.ready
	deps.ready = func(home string) ([]string, error) {
		lines, err := ready(home)
		if err == nil {
			if p.operationContext().Err() != nil {
				return nil, errors.Join(onboarding.ErrExit, p.operationContext().Err())
			}
			err = p.complete()
		}
		return lines, err
	}
}

func publishedSetupFailure(err error) error {
	if errors.Is(err, context.Canceled) {
		return errors.Join(onboarding.ErrExit, err)
	}
	if err == nil {
		return nil
	}
	var failure *setupFailure
	if errors.As(err, &failure) {
		return err
	}
	return &setupFailure{cause: setupSafeDiagnostic(err.Error())}
}

func (p *publishedSetup) stageInstalled() error {
	if err := copyPublishedFile(filepath.Join(p.root, "bin", "install.sh"), filepath.Join(p.home, "bin", "install.sh"), 0755); err != nil {
		return err
	}
	if err := distribution.Save(filepath.Join(p.home, "state", "pending-release.json"), p.record); err != nil {
		return err
	}
	return p.operation("prepared")
}

func (p *publishedSetup) finishExisting() error {
	if p.update != nil && p.update.BlocksWork() {
		return errors.New("The incomplete update requires recovery before completion.")
	}
	if os.Getenv("MC_BIN_DIR") != "" {
		if _, err := installSetupExecutable(); err != nil {
			return err
		}
	}
	pending, err := distribution.Load(filepath.Join(p.home, "state", "pending-release.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if pending.Commit != p.record.Commit {
		return errors.New("Another release requires recovery. Run install.sh to inspect the pending release.")
	}
	if installed, err := distribution.Load(filepath.Join(p.home, distribution.InstalledRecord)); err == nil {
		p.installed = installed
	}
	return p.complete()
}

func publishedRecoveryLines(home string) []string {
	lines := []string{}
	var resident struct {
		Release string `json:"releaseBuildId"`
	}
	installed, installedErr := distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	pending, pendingErr := distribution.Load(filepath.Join(home, "state", "pending-release.json"))
	if body, err := os.ReadFile(filepath.Join(home, "supervision", "resident.json")); err == nil && json.Unmarshal(body, &resident) == nil && validReleaseBuildID(resident.Release) {
		label := shortPublishedID(resident.Release)
		if pendingErr == nil && pending.Commit == resident.Release {
			label = pending.Label()
		} else if installedErr == nil && installed.Commit == resident.Release {
			label = installed.Label()
		}
		lines = append(lines, "Service release: "+terminalReleaseLabel(label)+".")
	}
	if installedErr == nil {
		lines = append(lines, "Last verified release: "+terminalReleaseLabel(installed.Label())+".")
	}
	if pendingErr == nil {
		lines = append(lines, "Release awaiting verification: "+terminalReleaseLabel(pending.Label())+".")
	}
	return lines
}

func (p *publishedSetup) complete() error {
	if p.root != "" {
		installed, err := deployment.ReadCLIInstallation(p.home)
		if err != nil {
			return err
		}
		actual, err := deployment.CLIFileDigest(installed.Path)
		if err != nil {
			return err
		}
		expected, err := deployment.CLIFileDigest(filepath.Join(p.root, "bin", "mc"))
		if err != nil || actual != expected || installed.Release != p.record.Commit || !installed.Verified {
			return errors.New("The command failed its release or shell check. Run install.sh again.")
		}
	}
	if err := distribution.Save(filepath.Join(p.home, distribution.InstalledRecord), p.record); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(p.home, "state", "pending-release.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if p.update != nil && !p.update.Committed() {
		if err := deployment.SyncUpdateDeployment(p.operationContext(), p.home, p.update, p.updateDocker); err != nil {
			return p.updateFailure(err)
		}
		if err := p.updatePhase("committed"); err != nil {
			return err
		}
	}
	if err := p.releaseMaintenance(); err != nil {
		return p.updateFailure(err)
	}
	p.hostBunProblem = ""
	if err := p.removePreviousHostBun(); err != nil {
		p.hostBunProblem = setupSafeDiagnostic(err.Error())
	}
	p.removeSupersededImages(context.Background())
	if p.update != nil && (p.hostBunProblem != "" || (p.images != nil && len(p.images.Problems) > 0)) {
		problems := []string{}
		if p.hostBunProblem != "" {
			problems = append(problems, p.hostBunProblem)
		}
		if p.images != nil {
			problems = append(problems, p.images.Problems...)
		}
		err := fmt.Errorf("The update is active. Run culler update to retry cleanup: %s", strings.Join(problems, "; "))
		return p.updateFailure(errors.Join(err, p.operation("cleanup")))
	}
	if err := p.operation("complete"); err != nil {
		return err
	}
	p.interrupted = nil
	if p.update != nil {
		return p.cleanupUpdate(p.operationContext())
	}
	return nil
}

func (p *publishedSetup) operation(phase string) error {
	state := map[string]any{
		"release": p.record.Commit, "phase": phase, "started_at": p.started.UTC(), "recorded_at": p.now().UTC(), "duration_seconds": p.now().Sub(p.started).Seconds(),
	}
	if p.images != nil {
		state["images"] = p.images
	}
	if p.hostBunProblem != "" {
		state["host_bun"] = map[string]string{"problem": p.hostBunProblem}
	}
	return writePublishedState(filepath.Join(p.home, "state", "install-operation.json"), state)
}

func (p *publishedSetup) stopAndRecheck(ctx context.Context, stop func(string) error) error {
	states, err := p.states(p.home)
	if err != nil {
		return err
	}
	stops, err := deployment.ReadServiceStops(p.home)
	if err != nil {
		return err
	}
	p.interrupted = &cullerOperation{Services: deployment.SupervisedUnits, Prior: states, Stops: stops}
	checkErr := stop(p.home)
	if checkErr == nil {
		database, err := p.database(ctx)
		checkErr = err
		if checkErr == nil && !database.Idle() {
			checkErr = errors.New("Culler work began before the services stopped. Finish that work, then run install.sh.")
		}
		if checkErr == nil {
			checkErr = p.requireIdleContainers(ctx, database.CollieContainers...)
		}
	}
	if checkErr != nil {
		p.releaseMaintenance()
		if p.restore != nil {
			return errors.Join(checkErr, p.restore(p.home, states))
		}
	}
	return checkErr
}

func restorePublishedServices(home string, states map[string]bool) error {
	return restorePublishedServicesWith(home, states, launchdController{})
}

// restorePublishedServicesWith starts each unit that ran before the update.
// A unit that does not start does not stop the start of the other units.
func restorePublishedServicesWith(home string, states map[string]bool, controller deployment.LaunchdController) error {
	names, err := deployment.RuntimeNames(home)
	if err != nil {
		return err
	}
	var problems []error
	for i := len(deployment.SupervisedUnits) - 1; i >= 0; i-- {
		unit := deployment.SupervisedUnits[i]
		if states[unit] {
			problems = append(problems, restorePublishedService(home, deployment.UnitLabel(names, unit), unit, controller))
		}
	}
	return errors.Join(problems...)
}

func restorePublishedService(home, label, unit string, controller deployment.LaunchdController) error {
	loaded, err := controller.Loaded(label)
	if err != nil || loaded {
		return err
	}
	if unit == "agentsview" {
		if err := deployment.InstallAgentsViewConfig(home); err != nil {
			return err
		}
	}
	return controller.Bootstrap(filepath.Join(home, "supervision", "LaunchAgents", unit+".plist"))
}

func copyPublishedFile(source, target string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("The published file is not a regular file.")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	output, err := os.CreateTemp(filepath.Dir(target), ".release-*")
	if err != nil {
		return err
	}
	defer os.Remove(output.Name())
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if err := output.Chmod(mode); err != nil {
		return err
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	if err := os.Rename(output.Name(), target); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(target))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (p *publishedSetup) prepare(ctx context.Context, report func(string)) error {
	if p.prepared {
		return nil
	}
	if prior, err := distribution.Load(filepath.Join(p.home, distribution.InstalledRecord)); err == nil {
		if p.update == nil {
			p.installed = prior
		}
		if prior.Sequence > p.record.Sequence {
			return errors.New("The installed release is newer. Setup left it intact.")
		}
		if prior.Sequence == p.record.Sequence && prior.Commit != p.record.Commit {
			return errors.New("The release order contains conflicting commits. Setup left the installed release intact.")
		}
		if prior.Commit == p.record.Commit && !samePublishedRecord(prior, p.record) {
			return errors.New("The published release changed its contents. Setup left the installed release intact.")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if pending, err := distribution.Load(filepath.Join(p.home, "state", "pending-release.json")); err == nil {
		if pending.Sequence > p.record.Sequence || pending.Sequence == p.record.Sequence && pending.Commit != p.record.Commit {
			return errors.New("A newer release requires recovery. Run the installer for that release.")
		}
		if pending.Commit == p.record.Commit && !samePublishedRecord(pending, p.record) {
			return errors.New("The proposed release differs from its recovery record. Setup left the deployment intact.")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if p.update != nil && p.update.Checkpoint != nil {
		report("Verify the retained agent image")
		if err := p.verifyRetainedImages(ctx); err != nil {
			return err
		}
	} else {
		report("Download the agent image")
		if err := p.pull(ctx, p.record, report); err != nil {
			return err
		}
	}
	report("Check database compatibility")
	if _, err := p.database(ctx); err != nil {
		return err
	}
	p.prepared = true
	return nil
}

func samePublishedRecord(a, b distribution.Record) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && bytes.Equal(left, right)
}

func pullPublishedImage(ctx context.Context, r distribution.Record, report func(string)) error {
	token, err := distribution.Credential(ctx)
	if err != nil {
		return err
	}
	prefix := []string{}
	if token != "" {
		client := distribution.NewClient("", token)
		username, err := client.Username(ctx)
		if err != nil {
			return err
		}
		directory, err := os.MkdirTemp("", "culler-registry-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(directory)
		endpoint, err := publishedDockerEndpoint(ctx)
		if err != nil {
			return err
		}
		prefix = []string{"--host", endpoint, "--config", directory}
		command := exec.CommandContext(ctx, "docker", append(prefix, "login", "ghcr.io", "--username", username, "--password-stdin")...)
		command.Stdin = strings.NewReader(token)
		if err := command.Run(); err != nil {
			return errors.New("The container registry refused access. Repair GitHub access, then run install.sh.")
		}
	}
	if r.ProjectImage == "" {
		return errors.New("The release has no default Project image. Run install.sh again after a complete release is available.")
	}
	for _, reference := range []string{r.Image, r.ProjectImage} {
		command := exec.CommandContext(ctx, "docker", append(prefix, "pull", reference)...)
		if err := command.Run(); err != nil {
			return errors.New("The image download failed. Check registry access, then run install.sh.")
		}
		body, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{json .}}", reference).Output()
		if err != nil {
			return err
		}
		selected := r
		selected.Image = reference
		if err := validatePublishedImage(body, selected); err != nil {
			return err
		}
	}
	report("The agent image passed its identity check")
	return nil
}

func publishedDockerEndpoint(ctx context.Context) (string, error) {
	endpoint := os.Getenv("DOCKER_HOST")
	if endpoint == "" || os.Getenv("DOCKER_CONTEXT") != "" {
		body, err := exec.CommandContext(ctx, "docker", "context", "inspect", "--format", "{{json .Endpoints.docker.Host}}").Output()
		if err != nil || json.Unmarshal(body, &endpoint) != nil {
			return "", errors.New("The Docker Desktop connection is unavailable. Open Docker Desktop, then retry install.sh.")
		}
	}
	if !strings.HasPrefix(endpoint, "unix:///") || strings.ContainsAny(endpoint, "\r\n\x00") {
		return "", errors.New("This installation requires a local Docker Desktop connection. Select that connection, then retry install.sh.")
	}
	return endpoint, nil
}

func validatePublishedImage(body []byte, r distribution.Record) error {
	var image struct {
		ID           string   `json:"Id"`
		OS           string   `json:"Os"`
		Architecture string   `json:"Architecture"`
		RepoDigests  []string `json:"RepoDigests"`
		Config       struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if json.Unmarshal(body, &image) != nil {
		return errors.New("The agent image identity is invalid.")
	}
	found := false
	for _, digest := range image.RepoDigests {
		found = found || digest == r.Image
	}
	if !found || image.OS != "linux" || image.Architecture != "arm64" || !strings.HasPrefix(image.ID, "sha256:") || !validContainerID(strings.TrimPrefix(image.ID, "sha256:")) || image.Config.Labels["org.opencontainers.image.revision"] != r.Commit {
		return errors.New("The agent image differs from the selected release.")
	}
	return nil
}

func (p *publishedSetup) database(ctx context.Context) (verbs.ReleaseDatabase, error) {
	r := verbs.ReleaseDatabase{Schema: p.record.Schema, Empty: true}
	names, err := deployment.RuntimeNames(p.home)
	if err != nil {
		return r, err
	}
	body, err := p.docker(ctx, "volume", "inspect", "--format", "{{json .}}", names.Volume)
	if err != nil && dockerObjectMissing(body) {
		if _, err := os.Stat(filepath.Join(p.home, "deployment.uuid")); os.IsNotExist(err) {
			return r, nil
		}
		return r, errors.New("The deployment database is absent. Restore compatible data before you run install.sh.")
	}
	if err != nil {
		return r, errors.New("The database volume check failed. Check Docker Desktop, then run install.sh.")
	}
	var volume helperVolumeInspect
	if json.Unmarshal(body, &volume) != nil || volume.Name != names.Volume || volume.Driver != "local" || len(volume.Options) != 0 {
		return r, errors.New("The deployment database volume identity is invalid.")
	}
	body, err = p.docker(ctx, "run", "--rm", "--network", "none", "--cap-drop", "ALL", "--user", "10002:10002",
		"--label", "mc-managed=true", "--label", "mc-deployment="+names.Suffix,
		"--mount", "type=volume,src="+names.Volume+",dst=/mc/spine,readonly", p.record.Image, "mc", "__release-database")
	if err != nil {
		return r, &setupFailure{cause: "This release cannot use the database. Setup left the installed release intact.", details: []string{setupSafeDiagnostic(string(body))}}
	}
	if json.Unmarshal(body, &r) != nil || r.Schema != p.record.Schema {
		return r, errors.New("The database check returned an invalid release identity.")
	}
	mirror, err := os.ReadFile(filepath.Join(p.home, "deployment.uuid"))
	if err == nil && (r.Empty || r.UUID != strings.TrimSpace(string(mirror))) {
		return r, errors.New("The database and deployment identities differ. Setup left the installed release intact.")
	}
	if err != nil && !os.IsNotExist(err) {
		return r, err
	}
	return r, nil
}

func (p *publishedSetup) drain(ctx context.Context) error {
	states, err := p.states(p.home)
	if err != nil {
		return err
	}
	_, recordErr := os.Stat(filepath.Join(p.home, distribution.InstalledRecord))
	legacy := os.IsNotExist(recordErr)
	if recordErr != nil && !legacy {
		return recordErr
	}
	if states["resident"] && (!legacy || p.forceMaintenance) {
		if p.maintenanceID == "" || !p.forceMaintenance {
			id := make([]byte, 16)
			if _, err := rand.Read(id); err != nil {
				return err
			}
			p.maintenanceID = hex.EncodeToString(id)
		}
		request := map[string]any{"id": p.maintenanceID, "requested_at": p.now().UTC().Format(time.RFC3339Nano)}
		if err := writePublishedState(filepath.Join(p.home, "state", "install-maintenance.json"), request); err != nil {
			return err
		}
	}
	deadline := p.now().Add(3 * time.Minute)
	for {
		ready := !states["resident"] || (legacy && !p.forceMaintenance)
		if !ready {
			body, err := os.ReadFile(filepath.Join(p.home, "state", "install-maintenance-ack.json"))
			var ack struct {
				ID         string    `json:"id"`
				Ready      bool      `json:"ready"`
				ObservedAt time.Time `json:"observed_at"`
			}
			ready = err == nil && json.Unmarshal(body, &ack) == nil && ack.ID == p.maintenanceID && ack.Ready && p.now().Sub(ack.ObservedAt) >= 0 && p.now().Sub(ack.ObservedAt) < 90*time.Second
		}
		if ready {
			database, err := p.database(ctx)
			if err != nil {
				return err
			}
			if database.Idle() {
				if err := p.requireIdleContainers(ctx, database.CollieContainers...); err == nil {
					return nil
				}
			}
		}
		if !p.now().Before(deadline) {
			return errors.New("Culler could not confirm idle work within three minutes. Check active sessions in the dashboard, then retry the operation.")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			p.sleep(time.Second)
		}
	}
}

func (p *publishedSetup) requireIdleContainers(ctx context.Context, collieIDs ...string) error {
	names, err := deployment.RuntimeNames(p.home)
	if err != nil {
		return err
	}
	for _, id := range collieIDs {
		if !validContainerID(id) {
			return errors.New("The Collie container identity is invalid.")
		}
		body, err := p.docker(ctx, "container", "inspect", "--format", "{{json .}}", id)
		if err != nil && dockerObjectMissing(body) {
			continue
		}
		if err != nil {
			return err
		}
		return errors.New("A Collie session still uses its agent container. Close the session, then retry install.sh.")
	}
	body, err := p.docker(ctx, "container", "ls", "--all", "--no-trunc", "--quiet", "--filter", "volume="+names.Volume)
	if err != nil {
		return err
	}
	for _, id := range strings.Fields(string(body)) {
		body, err := p.docker(ctx, "container", "inspect", "--format", "{{json .}}", id)
		if err != nil {
			return err
		}
		var container helperContainerInspect
		if json.Unmarshal(body, &container) != nil || container.ID != id || strings.TrimPrefix(container.Name, "/") != names.Helper || !helperOwned(container) {
			return errors.New("An agent still uses the deployment database.")
		}
	}
	return nil
}

func writePublishedState(path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".install-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(append(body, '\n'))
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (p *publishedSetup) runAutomatic(raw []string, a verbs.OnboardArgs, stdin io.Reader, stdout, stderr io.Writer) int {
	if p.update != nil {
		return p.runUpdate(raw, a, stdin, stdout, stderr)
	}
	state, err := inspectSetupDeployment(p.home, a)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	requested := a
	requested.ActivateSupervision = false
	if !p.resumingUpdate && (state.Release.Relation == "older" || state.complete() && !setupRequestedChanges(requested)) {
		if state.Release.Relation != "older" {
			if err := p.finishExisting(); err != nil {
				return writeVerbError(stdout, stderr, err)
			}
		}
		_ = writeJSON(stdout, map[string]any{"ok": true, "release": state.Release.Installed, "changed": false})
		return 0
	}
	if state.Exists {
		if _, err := os.Stat(filepath.Join(p.home, distribution.InstalledRecord)); err == nil {
			return p.runUpdate(raw, a, stdin, stdout, stderr)
		}
	}
	if state.Exists && !p.resumingUpdate && !slices.Contains(raw, "--interrupt-runs") {
		return writeVerbError(stdout, stderr, verbs.Usagef("Use --interrupt-runs to authorize interruption of active work during an unattended update."))
	}
	if !a.ActivateSupervision {
		return writeVerbError(stdout, stderr, verbs.Usagef("Use install.sh --yes --activate to authorize service activation."))
	}
	wholeArgs, err := p.automaticArgs(raw, a, state.Exists)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	resolved, err := parseOnboardArgs(wholeArgs)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if _, err := validateWholeOnboardChoices(resolved); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	ctx, cancel := context.WithTimeout(p.operationContext(), 30*time.Minute)
	defer cancel()
	if err := p.assessCandidate(ctx); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if state.Exists {
		if err := p.beforeUpdate(ctx, func(line string) { fmt.Fprintln(stderr, line) }); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	} else if err := p.prepare(ctx, func(line string) { fmt.Fprintln(stderr, line) }); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	program, err := installSetupExecutable()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := p.stageInstalled(); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := p.installAgentsView(); err != nil {
		return writeVerbError(stdout, stderr, publishedAgentsViewFailure(err, state.Exists))
	}
	var result bytes.Buffer
	previous := productionWholeRunSection
	defer func() { productionWholeRunSection = previous }()
	productionWholeRunSection = func(args []string, input io.Reader, output, diagnostic io.Writer) int {
		if p.update != nil && len(args) > 1 && args[1] == "supervision" {
			if err := p.updatePhase("verify"); err != nil {
				return writeVerbError(output, diagnostic, err)
			}
		}
		command := exec.CommandContext(ctx, program, args...)
		command.Stdin, command.Stdout, command.Stderr = input, output, diagnostic
		if err := command.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				return exit.ExitCode()
			}
			return 2
		}
		return 0
	}
	code := brokerWholeOnboard(append([]string{"__onboard"}, wholeArgs...), stdin, &result, stderr)
	if code != 0 {
		_, _ = stdout.Write(result.Bytes())
		return code
	}
	if p.update != nil {
		if err := p.updatePhase("verify"); err != nil {
			return writeVerbError(stdout, stderr, err)
		}
	}
	if _, err := setupFinalReadiness(p.home, a); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := p.complete(); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	_, _ = stdout.Write(result.Bytes())
	return 0
}

func (p *publishedSetup) automaticArgs(args []string, a verbs.OnboardArgs, existing bool) ([]string, error) {
	result := []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--yes", "--interrupt-runs", "--restore":
			continue
		case "--mc-home", "--bin-dir":
			i++
			continue
		case "--acquire-runtime-auth":
			result = append(result, "--acquire")
		default:
			result = append(result, args[i])
		}
	}
	if !existing {
		return result, nil
	}
	cfg, err := operatorconfig.Load(filepath.Join(p.home, "config.toml"))
	if err != nil {
		return nil, err
	}
	if a.RuntimeBindings == "" {
		result = append(result, "--runtime-bindings", strings.Join(cfg.Runtimes.EnabledBindings, ","))
	}
	if !a.AcceptDefaultRouting && a.RoutingFile == "" {
		result = append(result, "--routing-file", filepath.Join(p.home, "routing.md"))
	}
	if !a.AcceptDefaultGitIdentity && a.GitAuthorName == "" && a.GitAuthorEmail == "" {
		result = append(result, "--git-author-name", cfg.Git.AuthorName, "--git-author-email", cfg.Git.AuthorEmail)
	}
	if !a.AcceptDefaultDashboardBind && a.DashboardBind == "" {
		result = append(result, "--dashboard-bind", cfg.Surfaces.Dashboard.Bind)
	}
	if !a.TunablesConfirmed {
		for _, field := range setupTunableFields(cfg) {
			if setupTunableAnswers(a)[field.name] == 0 {
				result = append(result, "--"+field.name, fmt.Sprint(field.value))
			}
		}
	}
	return result, nil
}

func publishedSurfacesConfigured() bool {
	if os.Getenv("CULLER_RELEASE_RECORD") == "" {
		return false
	}
	home, err := configuredCanonicalHome()
	if err != nil {
		return false
	}
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	return err == nil && cfg.Surfaces.Dashboard.AuthToken != ""
}
