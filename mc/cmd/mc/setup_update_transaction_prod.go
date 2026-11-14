//go:build !test_fake_routing

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"mc/deployment"
	"mc/distribution"
	"mc/onboarding"
	"mc/verbs"
)

func (p *publishedSetup) updateDocker(ctx context.Context, input string, args ...string) ([]byte, error) {
	if input == "" && p.docker != nil {
		return p.docker(ctx, args...)
	}
	command := exec.CommandContext(ctx, publishedUpdateDockerProgram(p.home), args...)
	command.Stdin = strings.NewReader(input)
	return command.CombinedOutput()
}

func (p *publishedSetup) retainCandidate() error {
	if p.update != nil || p.stagedUpdate != nil {
		return nil
	}
	previous, err := distribution.Load(filepath.Join(p.home, distribution.InstalledRecord))
	if errors.Is(err, os.ErrNotExist) {
		if _, configErr := os.Stat(filepath.Join(p.home, "config.toml")); configErr == nil {
			return errors.New("This deployment predates public updates. Install the first public release in a new deployment home.")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if previous.Commit == p.record.Commit && !samePublishedRecord(previous, p.record) {
		return errors.New("The published release changed its contents. The installed release remains intact.")
	}
	if previous.Sequence == p.record.Sequence && previous.Commit != p.record.Commit {
		return errors.New("The release order contains conflicting commits. The installed release remains intact.")
	}
	if err := distribution.ValidateUpdateSource(p.record, previous); err != nil {
		return err
	}
	installation, err := deployment.ReadCLIInstallation(p.home)
	if err != nil {
		return err
	}
	if filepath.Dir(installation.Path) != os.Getenv("MC_BIN_DIR") || !installation.Verified {
		return errors.New("A public update keeps the existing command directory. Use its saved installer without a different --bin-dir.")
	}
	names, err := deployment.RuntimeNames(p.home)
	if err != nil {
		return err
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	identity := hex.EncodeToString(id)
	j := &deployment.UpdateJournal{Version: 1, ID: identity, Deployment: names.Suffix, Phase: "stop", Candidate: p.record,
		Previous: &previous, CandidateRoot: filepath.Join(deployment.UpdateDirectory(p.home, identity), "candidate"),
		BinDir: os.Getenv("MC_BIN_DIR"), PriorServices: map[string]bool{}, PriorStops: deployment.ServiceStops{}, StartedAt: time.Now().UTC()}
	p.stagedUpdate = j
	if err := deployment.RetainUpdateCandidate(p.home, j, p.root); err != nil {
		return err
	}
	p.stagedUpdate = j
	return nil
}

func (p *publishedSetup) beginUpdate() error {
	if p.update != nil {
		return nil
	}
	if err := p.retainCandidate(); err != nil {
		return err
	}
	if p.stagedUpdate == nil {
		return nil
	}
	j := p.stagedUpdate
	states, err := p.states(p.home)
	if err != nil {
		return err
	}
	stops, err := deployment.ReadServiceStops(p.home)
	if err != nil {
		return err
	}
	j.PriorServices, j.PriorStops = states, stops
	if p.candidateAssessment != nil {
		j.ConfigSHA256 = p.candidateAssessment.configSHA256()
	}
	if err := deployment.SaveUpdateJournal(p.home, j); err != nil {
		return err
	}
	p.update, p.stagedUpdate = j, nil
	p.maintenanceID = j.ID
	p.installed = *j.Previous
	return nil
}

func (p *publishedSetup) updatePhase(phase string) error {
	if p.update == nil {
		return nil
	}
	next := *p.update
	next.Phase, next.Problem = phase, ""
	if err := deployment.SaveUpdateJournal(p.home, &next); err != nil {
		return err
	}
	*p.update = next
	return nil
}

func (p *publishedSetup) updateFailure(err error) error {
	if err == nil || p.update == nil {
		return err
	}
	p.update.Problem = setupSafeDiagnostic(err.Error())
	return errors.Join(err, deployment.SaveUpdateJournal(p.home, p.update))
}

func (p *publishedSetup) beforeUpdate(ctx context.Context, report func(string)) error {
	if err := p.beginUpdate(); err != nil {
		return err
	}
	if p.update == nil {
		return p.prepare(ctx, report)
	}
	if p.update.Committed() {
		return p.cleanupUpdate(ctx)
	}
	if err := p.updatePhase("stop"); err != nil {
		return err
	}
	if err := p.stopForUpdate(ctx, report); err != nil {
		return p.updateFailure(err)
	}
	if err := p.updatePhase("prepare"); err != nil {
		return err
	}
	if err := p.prepare(ctx, report); err != nil {
		return p.updateFailure(err)
	}
	if err := p.verifyCandidateConfiguration(); err != nil {
		return p.updateFailure(err)
	}
	if err := p.retainOpenCodeArchive(ctx, report); err != nil {
		return p.updateFailure(err)
	}
	if err := p.checkCandidateProjectImages(ctx); err != nil {
		return p.updateFailure(err)
	}
	if p.update.Checkpoint == nil {
		if err := p.updatePhase("checkpoint"); err != nil {
			return err
		}
		report("Protect the previous release and saved data")
		if err := deployment.CreateUpdateCheckpoint(ctx, p.home, p.update, p.updateDocker); err != nil {
			return p.updateFailure(err)
		}
		if err := deployment.SaveUpdateJournal(p.home, p.update); err != nil {
			return err
		}
	}
	if err := p.updatePhase("replace"); err != nil {
		return err
	}
	return p.stageInstalled()
}

func (p *publishedSetup) cleanupUpdate(ctx context.Context) error {
	if p.update == nil {
		return nil
	}
	if !p.update.Committed() {
		return errors.New("The update requires verification before cleanup.")
	}
	if err := deployment.CleanupUpdateCheckpoint(ctx, p.home, p.update, p.updateDocker); err != nil {
		return p.updateFailure(fmt.Errorf("The update succeeded, but cleanup requires a retry: %w", err))
	}
	if err := p.updatePhase("complete"); err != nil {
		return err
	}
	return deployment.RemoveUpdateArtifacts(p.home, p.update)
}

func (p *publishedSetup) recoverUpdate(raw []string, a verbs.OnboardArgs, in io.Reader, out, diagnostic io.Writer) (int, bool) {
	if p.update == nil {
		return 0, false
	}
	if p.update.Phase == "complete" || p.update.Phase == "restored" {
		if slices.Contains(raw, "--restore") {
			return writeVerbError(out, diagnostic, errors.New("This update has no incomplete restoration.")), true
		}
		return 0, false
	}
	restore := slices.Contains(raw, "--restore")
	if p.update.Committed() {
		if restore {
			return writeVerbError(out, diagnostic, errors.New("The update already committed. Recovery must preserve current data.")), true
		}
		err := p.complete()
		if err != nil {
			return writeVerbError(out, diagnostic, err), true
		}
		fmt.Fprintln(out, "The update cleanup is complete.")
		return 0, true
	}
	assessmentErr := p.assessCandidate(p.operationContext())
	if !slices.Contains(raw, "--yes") {
		terminal := onboarding.NewTerminal(in, out)
		if !terminal.HasTerminal() {
			return writeVerbError(out, diagnostic, errors.New("Update recovery requires a terminal or explicit --yes --interrupt-runs consent.")), true
		}
		if assessmentErr != nil {
			terminal.Panel("The saved configuration requires repair", []string{setupSafeDiagnostic(assessmentErr.Error()), "Restore remains available."})
		}
		choice, err := p.confirmRecovery(terminal, restore)
		if err != nil {
			return writeVerbError(out, diagnostic, err), true
		}
		if choice == "exit" {
			return 0, true
		}
		restore = choice == "restore"
	} else if !slices.Contains(raw, "--interrupt-runs") {
		return writeVerbError(out, diagnostic, errors.New("Use --interrupt-runs to authorize interruption during update recovery.")), true
	}
	if restore {
		if err := p.restoreUpdate(p.operationContext()); err != nil {
			return writeVerbError(out, diagnostic, p.updateFailure(err)), true
		}
		fmt.Fprintln(out, "The previous release and saved data are restored.")
		return 0, true
	}
	if assessmentErr != nil {
		return writeVerbError(out, diagnostic, assessmentErr), true
	}
	if slices.Contains(raw, "--yes") {
		for _, line := range append(p.candidateAssessment.Lines(), p.candidateAssessment.Waits()...) {
			fmt.Fprintln(diagnostic, line)
		}
	}
	p.resumingUpdate = true
	a.ActivateSupervision = true
	args := append(append([]string{}, raw...), "--yes", "--activate", "--interrupt-runs")
	return p.runAutomatic(args, a, in, out, diagnostic), true
}

func startReleaseHelper(home, image string) error {
	names, err := deployment.RuntimeNames(home)
	if err != nil {
		return err
	}
	_, err = (&helperManager{docker: execDockerRunner{}, imageRef: image, names: names, zone: hostZone()}).ensure()
	return err
}

func (p *publishedSetup) restoreUpdate(ctx context.Context) error {
	if p.update == nil || p.update.Previous == nil || p.update.Committed() {
		return errors.New("This update has no previous release available for restoration.")
	}
	if err := p.updatePhase("restore"); err != nil {
		return err
	}
	if err := p.stopForUpdate(ctx, func(string) {}); err != nil {
		return err
	}
	if p.update.Checkpoint != nil {
		if err := deployment.RestoreUpdateCheckpoint(ctx, p.home, p.update, p.updateDocker); err != nil {
			return err
		}
	}
	if err := deployment.WriteServiceStops(p.home, p.update.PriorStops); err != nil {
		return err
	}
	// The update stop removed the helper container, and the resident verification reads the spine through it.
	if err := p.helper(p.home, p.update.Previous.Image); err != nil {
		return fmt.Errorf("The restore could not start the helper container, so work remains stopped. Correct the cause, then run culler update --restore. Cause: %w", err)
	}
	if err := p.restore(p.home, p.update.PriorServices); err != nil {
		return err
	}
	if err := p.verifyUpdateServices(ctx, *p.update.Previous, p.update.PriorServices); err != nil {
		return err
	}
	if err := p.updatePhase("restored"); err != nil {
		return err
	}
	if err := p.releaseMaintenance(); err != nil {
		return err
	}
	if err := deployment.CleanupUpdateCheckpoint(ctx, p.home, p.update, p.updateDocker); err != nil {
		return err
	}
	return deployment.RemoveUpdateArtifacts(p.home, p.update)
}

func (p *publishedSetup) verifyUpdateServices(ctx context.Context, record distribution.Record, services map[string]bool) error {
	runtime, err := readCullerRuntime(p.home)
	if err != nil {
		return err
	}
	if runtime.Release != record.Commit || runtime.Image != record.Image {
		return errors.New("The service configuration differs from the selected release.")
	}
	if err := inspectCullerCommandFiles(p.home); err != nil {
		return err
	}
	for _, unit := range deployment.SupervisedUnits {
		if !services[unit] {
			continue
		}
		deadline := time.Now().Add(60 * time.Second)
		for {
			err = cullerReadyForUpdate(ctx, p.home, unit, runtime)
			if err == nil {
				break
			}
			if !time.Now().Before(deadline) {
				return fmt.Errorf("The %s service failed verification: %w", unit, err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	return nil
}

func cullerReadyForUpdate(ctx context.Context, home, unit string, runtime cullerRuntime) error {
	if unit == "dashboard" {
		return setupCheckDashboard(home)
	}
	if unit != "resident" {
		return cullerReady(ctx, home, unit, runtime, time.Time{})
	}
	if err := deployment.InspectResidentReceipt(home, runtime.Release, time.Time{}, time.Now()); err != nil {
		return err
	}
	if !verbs.ResidentAvailability(home, time.Now()).Available {
		return errors.New("The resident has no current response.")
	}
	return nil
}

func (p *publishedSetup) verifyRetainedImages(ctx context.Context) error {
	for _, reference := range []string{p.record.Image, p.record.ProjectImage} {
		if reference == "" {
			return errors.New("The retained release lacks a required image.")
		}
		body, err := p.docker(ctx, "image", "inspect", "--format", "{{json .}}", reference)
		if err != nil {
			return fmt.Errorf("A retained image is unavailable: %s", reference)
		}
		selected := p.record
		selected.Image = reference
		if err := validatePublishedImage(body, selected); err != nil {
			return err
		}
	}
	return nil
}

func (p *publishedSetup) candidateOpenCodeArchive() string {
	if p.update == nil || p.record.OpenCode == nil {
		return ""
	}
	return filepath.Join(deployment.UpdateDirectory(p.home, p.update.ID), p.record.OpenCode.Name)
}

func (p *publishedSetup) retainOpenCodeArchive(ctx context.Context, report func(string)) error {
	if p.update == nil {
		return nil
	}
	volume, err := deployment.OpenCodeVolume(p.home)
	if err != nil {
		return err
	}
	body, err := p.docker(ctx, "volume", "inspect", volume)
	if err != nil {
		if dockerObjectMissing(body) {
			return nil
		}
		return errors.New("The optional OpenCode volume could not be inspected.")
	}
	if p.record.OpenCode == nil {
		return errors.New("The candidate lacks the required OpenCode archive.")
	}
	archive := p.candidateOpenCodeArchive()
	if body, err := os.ReadFile(archive); err == nil {
		digest := sha256.Sum256(body)
		if hex.EncodeToString(digest[:]) == p.record.OpenCode.SHA256 && int64(len(body)) == p.record.OpenCode.Size {
			return nil
		}
	}
	if p.update.Checkpoint != nil {
		return errors.New("The retained OpenCode archive is absent or invalid. Keep Culler stopped and repair the recovery files.")
	}
	token, err := distribution.Credential(ctx)
	if err != nil {
		return err
	}
	client := distribution.NewClient("", token)
	client.HTTP.Timeout = 15 * time.Minute
	report("Retain the OpenCode archive for this update")
	if err := os.Remove(archive); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := client.Download(ctx, p.record, *p.record.OpenCode, archive); err != nil {
		return err
	}
	file, err := os.OpenFile(archive, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	err = file.Sync()
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(archive))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func runRetainedUpdate(ctx context.Context, home string, restore, noColor bool, in io.Reader, out, diagnostic io.Writer) (int, bool) {
	j, err := deployment.LoadUpdateJournal(home)
	if err != nil {
		return writeVerbError(out, diagnostic, err), true
	}
	if j != nil && (j.Phase == "complete" || j.Phase == "restored") {
		lock, err := lockPublishedSetup(home)
		if err != nil {
			return writeVerbError(out, diagnostic, err), true
		}
		cleanupErr := finishUpdateCleanup(ctx, home, j)
		_ = lock.Close()
		if cleanupErr != nil {
			return writeVerbError(out, diagnostic, cleanupErr), true
		}
	}

	if j == nil || j.Phase == "complete" || j.Phase == "restored" {
		if restore {
			return writeVerbError(out, diagnostic, errors.New("This deployment has no incomplete update to restore.")), true
		}
		return 0, false
	}
	if err := deployment.RetainUpdateCandidate(home, j, j.CandidateRoot); err != nil {
		return writeVerbError(out, diagnostic, err), true
	}
	program := filepath.Join(j.CandidateRoot, "bin", "mc")
	digest, err := deployment.CLIFileDigest(program)
	if err != nil || digest != j.CandidateSHA256 {
		return writeVerbError(out, diagnostic, errors.New("The retained update executable is absent or invalid. Keep Culler stopped and repair the recovery files.")), true
	}
	args := []string{"__setup", "--mc-home", home, "--bin-dir", j.BinDir, "--release-source", filepath.Join(j.CandidateRoot, "runner"), "--host-release-source", filepath.Join(j.CandidateRoot, "host")}
	if restore {
		args = append(args, "--restore")
	}
	command := exec.CommandContext(ctx, program, args...)
	command.Cancel = func() error { return command.Process.Signal(os.Interrupt) }
	command.Stdin, command.Stdout, command.Stderr = in, out, diagnostic
	environment := os.Environ()
	for _, prefix := range []string{"MC_HOME=", "MC_BIN_DIR=", "CULLER_PACKAGE_ROOT=", "CULLER_RELEASE_RECORD=", "NO_COLOR="} {
		environment = slices.DeleteFunc(environment, func(v string) bool { return strings.HasPrefix(v, prefix) })
	}
	command.Env = append(environment, "MC_HOME="+home, "MC_BIN_DIR="+j.BinDir, "CULLER_PACKAGE_ROOT="+j.CandidateRoot, "CULLER_RELEASE_RECORD="+filepath.Join(j.CandidateRoot, "release.json"))
	if noColor {
		command.Env = append(command.Env, "NO_COLOR=1")
	}
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return 130, true
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode(), true
		}
		return writeVerbError(out, diagnostic, err), true
	}
	return 0, true
}

func (p *publishedSetup) offerUpdate(raw []string, a verbs.OnboardArgs, in io.Reader, out, diagnostic io.Writer) (int, bool) {
	previous, err := distribution.Load(filepath.Join(p.home, distribution.InstalledRecord))
	if errors.Is(err, os.ErrNotExist) {
		return 0, false
	}
	if err != nil {
		return writeVerbError(out, diagnostic, err), true
	}
	if err := validatePublicUpdateOptions(a); err != nil {
		return writeVerbError(out, diagnostic, err), true
	}
	if samePublishedRecord(previous, p.record) {
		return 0, false
	}
	if err := p.retainCandidate(); err != nil {
		return writeVerbError(out, diagnostic, err), true
	}
	if err := p.assessCandidate(p.operationContext()); err != nil {
		return writeVerbError(out, diagnostic, err), true
	}
	if slices.Contains(raw, "--yes") {
		return p.runUpdate(raw, a, in, out, diagnostic), true
	}
	terminal := onboarding.NewTerminal(in, out)
	if !terminal.HasTerminal() {
		return writeVerbError(out, diagnostic, errors.New("The update requires a terminal or explicit --yes --activate --interrupt-runs consent.")), true
	}
	choice, err := p.confirmUpdate(terminal)
	if err != nil {
		return writeVerbError(out, diagnostic, err), true
	}
	if choice != "update" {
		return 0, true
	}
	a.ActivateSupervision = true
	return p.runUpdate(append(raw, "--interrupt-runs"), a, in, out, diagnostic), true
}

func (p *publishedSetup) confirmUpdate(terminal setupTerminal) (string, error) {
	lines := []string{"This update interrupts active runs.", "Saved data remains. Interrupted work can require a retry.", "Culler permits work after verification.", "Your service stop choices remain."}
	if p.candidateAssessment != nil && len(p.candidateAssessment.Lines()) > 0 {
		terminal.Panel("Saved choices that require repair", p.candidateAssessment.Lines())
		lines = append(lines, "Affected active profiles stay stopped until repair in the dashboard.")
	}
	lines = append(lines, p.candidateAssessment.Waits()...)
	lines = append(lines, "Selected release: "+p.record.Version+" ("+shortPublishedID(p.record.Commit)+").")
	terminal.Panel("Update Culler", lines)
	return terminal.Choose("Choose an action", []onboarding.Choice{setupChoice("update", "Stop and update", "Apply the selected release."), setupChoice("cancel", "Cancel", "Keep the current deployment.")}, 0)
}

func (p *publishedSetup) runUpdate(raw []string, a verbs.OnboardArgs, in io.Reader, out, diagnostic io.Writer) int {
	if err := validatePublicUpdateOptions(a); err != nil {
		return writeVerbError(out, diagnostic, err)
	}
	if !slices.Contains(raw, "--interrupt-runs") {
		return writeVerbError(out, diagnostic, errors.New("Use --interrupt-runs to authorize interruption of active work during an unattended update."))
	}
	if !a.ActivateSupervision {
		return writeVerbError(out, diagnostic, errors.New("Use --activate to authorize service activation after the update."))
	}
	if p.candidateAssessment == nil {
		if err := p.assessCandidate(p.operationContext()); err != nil {
			return writeVerbError(out, diagnostic, err)
		}
	} else if err := p.verifyCandidateConfiguration(); err != nil {
		return writeVerbError(out, diagnostic, err)
	}
	if p.candidateAssessment == nil || !p.candidateAssessment.present {
		return writeVerbError(out, diagnostic, errors.New("The saved configuration is absent. Restore the configuration before a public update."))
	}
	if p.update != nil {
		next := *p.update
		next.ConfigSHA256 = p.candidateAssessment.configSHA256()
		if err := deployment.SaveUpdateJournal(p.home, &next); err != nil {
			return writeVerbError(out, diagnostic, err)
		}
		*p.update = next
	}
	ctx, cancel := context.WithTimeout(p.operationContext(), 30*time.Minute)
	defer cancel()
	docker := onboarding.DetectDocker(ctx)
	if docker.State != onboarding.DockerReady {
		return writeVerbError(out, diagnostic, errors.New(docker.Detail))
	}
	if docker.CommandPath != "" {
		if err := os.Setenv("PATH", filepath.Dir(docker.CommandPath)+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
			return writeVerbError(out, diagnostic, err)
		}
	}
	if err := p.beforeUpdate(ctx, func(line string) { fmt.Fprintln(diagnostic, line) }); err != nil {
		return writeVerbError(out, diagnostic, err)
	}
	program, err := installSetupExecutable()
	if err != nil {
		return writeVerbError(out, diagnostic, p.updateFailure(err))
	}
	if err := p.installAgentsView(); err != nil {
		return writeVerbError(out, diagnostic, p.updateFailure(err))
	}
	args := verbs.OnboardArgs{ReleaseSource: filepath.Join(p.root, "runner"), HostReleaseSource: filepath.Join(p.root, "host"), ActivateSupervision: true}
	runSection := func(sectionArgs []string) error {
		command := exec.CommandContext(ctx, program, sectionArgs...)
		var result bytes.Buffer
		command.Stdin, command.Stdout, command.Stderr = in, &result, diagnostic
		return command.Run()
	}
	if err := p.applyUpdateSections(args, runSection); err != nil {
		return writeVerbError(out, diagnostic, p.updateFailure(err))
	}
	if _, err := setupFinalReadiness(p.home, args); err != nil {
		return writeVerbError(out, diagnostic, p.updateFailure(err))
	}
	if err := p.complete(); err != nil {
		return writeVerbError(out, diagnostic, err)
	}
	fmt.Fprintln(out, "The update is complete. Your service stop choices remain in effect.")
	return 0
}

func (p *publishedSetup) applyUpdateSections(args verbs.OnboardArgs, runSection func([]string) error) error {
	for _, section := range []string{"home", "container", "supervision"} {
		if section == "supervision" {
			if err := p.updatePhase("verify"); err != nil {
				return err
			}
		}
		if err := runSection(wholeSectionArgs(section, args)); err != nil {
			return fmt.Errorf("The update could not complete %s: %w", section, err)
		}
	}
	return nil
}

func (p *publishedSetup) confirmRecovery(terminal setupTerminal, restore bool) (string, error) {
	lines := []string{"Culler keeps work stopped until recovery completes.", "Retry uses the same verified release.", "Restore returns to the previous release and its saved data."}
	if p.candidateAssessment != nil && len(p.candidateAssessment.Lines()) > 0 {
		terminal.Panel("Saved choices that require repair", p.candidateAssessment.Lines())
		lines = append(lines, "Affected active profiles stay stopped until repair in the dashboard.")
	}
	lines = append(lines, p.candidateAssessment.Waits()...)
	terminal.Panel("Recover the interrupted update", lines)
	choices := []onboarding.Choice{setupChoice("retry", "Retry the update", "Use the retained release."), setupChoice("exit", "Exit", "Keep Culler stopped.")}
	if p.update.Previous != nil {
		choices = append(choices, setupChoice("restore", "Restore the previous release", "Use the saved recovery data."))
	}
	selected := 0
	if restore && len(choices) > 2 {
		selected = 2
	}
	return terminal.Choose("Select recovery", choices, selected)
}

func validatePublicUpdateOptions(args verbs.OnboardArgs) error {
	args.ActivateSupervision = false
	if setupRequestedChanges(args) {
		return errors.New("A public update preserves the saved configuration. Apply configuration changes through the dashboard after the update.")
	}
	return nil
}

func publishedUpdateDockerProgram(home string) string {
	if runtime, err := readCullerRuntime(home); err == nil && runtime.Docker != "" {
		return runtime.Docker
	}
	return "docker"
}

func finishUpdateCleanup(ctx context.Context, home string, j *deployment.UpdateJournal) error {
	current, err := deployment.LoadUpdateJournal(home)
	if err != nil {
		return err
	}
	if current == nil || current.ID != j.ID || current.Phase != j.Phase {
		return errors.New("The update record changed. Run culler update again.")
	}
	p := &publishedSetup{home: home, update: j, maintenanceID: j.ID}
	if err := p.releaseMaintenance(); err != nil {
		return err
	}
	if _, err := os.Stat(deployment.UpdateDirectory(home, j.ID)); err == nil {
		if err := deployment.CleanupUpdateCheckpoint(ctx, home, j, nil); err != nil {
			return err
		}
		return deployment.RemoveUpdateArtifacts(home, j)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func checkUpdateDashboardIdentity(home, identity string) (bool, error) {
	j, err := deployment.LoadUpdateJournal(home)
	if err != nil {
		return false, err
	}
	if j == nil || (j.Phase != "verify" && j.Phase != "restore") {
		return false, nil
	}
	expected := j.Candidate.Commit
	stops, err := deployment.ReadServiceStops(home)
	if err != nil {
		return true, err
	}
	requireResident := !stops["resident"]
	if j.Phase == "restore" {
		if j.Previous == nil {
			return true, errors.New("The previous release identity is absent.")
		}
		expected = j.Previous.Commit
		requireResident = j.PriorServices["resident"]
	}
	if identity != expected {
		return true, errors.New("The dashboard response differs from the selected release.")
	}
	if requireResident {
		if err := deployment.InspectResidentReceipt(home, expected, time.Time{}, time.Now()); err != nil {
			return true, err
		}
		availability := verbs.ResidentAvailability(home, time.Now())
		if !availability.Available || availability.CanCommit {
			return true, errors.New("The resident must answer while update maintenance keeps work stopped.")
		}
	}
	return true, nil
}
