//go:build !test_fake_routing

package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/opencodecatalog"
)

// The Project image smoke runs the same file against the default Project image.
//
//go:embed candidate-runtime-check.sh
var candidateRuntimeCheck string

type updateCandidateAssessment struct {
	digest  string
	present bool
	lines   []string
	// The enabled profiles whose live model another release derived.
	waits []string
}

// Lines returns the saved choices that require repair.
func (a *updateCandidateAssessment) Lines() []string {
	if a == nil {
		return nil
	}
	return append([]string(nil), a.lines...)
}

// Waits returns the statement about the enabled profiles whose live model the update derives again.
func (a *updateCandidateAssessment) Waits() []string {
	if a == nil || len(a.waits) == 0 {
		return nil
	}
	count := len(a.waits)
	if count == 1 {
		return []string{fmt.Sprintf("Agent profile %q uses a model from the live model list of OpenCode. The update starts a refresh of the live models. If the refresh fails or the list lacks the model, work that uses this profile stays stopped until repair in the dashboard.", a.waits[0])}
	}
	names := make([]string, count)
	for i, id := range a.waits {
		names[i] = fmt.Sprintf("%q", id)
	}
	list := names[0] + " and " + names[1]
	if count > 2 {
		list = strings.Join(names[:count-1], ", ") + ", and " + names[count-1]
	}
	return []string{"Agent profiles " + list + " use models from the live model list of OpenCode. The update starts a refresh of the live models. For each of these profiles, if the refresh fails or the list lacks its model, work that uses that profile stays stopped until repair in the dashboard."}
}

func (a *updateCandidateAssessment) configSHA256() string {
	if a == nil {
		return ""
	}
	return a.digest
}

func assessUpdateConfiguration(home string) (*updateCandidateAssessment, error) {
	path := filepath.Join(home, "config.toml")
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &updateCandidateAssessment{}, nil
	}
	if err != nil {
		return nil, err
	}
	cfg, err := operatorconfig.Load(path)
	if err != nil {
		return nil, fmt.Errorf("The candidate cannot read the saved configuration: %w", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(body) != string(after) {
		return nil, errors.New("The configuration changed during assessment. Run install.sh again.")
	}
	digest := sha256.Sum256(body)
	a := &updateCandidateAssessment{digest: hex.EncodeToString(digest[:]), present: true}
	ids := make([]string, 0, len(cfg.Runtimes.Bindings))
	for id := range cfg.Runtimes.Bindings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	enabled := map[string]bool{}
	for _, id := range cfg.Runtimes.EnabledBindings {
		enabled[id] = true
	}
	for _, id := range ids {
		profile := cfg.Runtimes.Bindings[id]
		if profile.Harness != "opencode" {
			continue
		}
		connection := cfg.Runtimes.Credentials[profile.Credential]
		if err := deployment.ValidateOpenCodeProfile(home, connection.CatalogEntry, profile.Model, profile.Effort); err != nil {
			// The candidate derives the live models again after the update.
			if deployment.OpenCodeProfileAwaitsRefresh(home, connection.CatalogEntry, profile.Model, profile.Effort) {
				if enabled[id] {
					a.waits = append(a.waits, id)
				}
				continue
			}
			state := "The disabled profile keeps its saved choices."
			if enabled[id] {
				state = "Work that uses this profile stays stopped until repair in the dashboard."
			}
			a.lines = append(a.lines, fmt.Sprintf("Agent profile %q: %s %s", id, err.Error(), state))
		}
	}
	ids = ids[:0]
	for id := range cfg.Runtimes.Credentials {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		connection := cfg.Runtimes.Credentials[id]
		if connection.Kind == operatorconfig.CredentialKindOpenCodeAPIKey {
			if _, ok := opencodecatalog.Find(connection.CatalogEntry); !ok {
				a.lines = append(a.lines, fmt.Sprintf("Connection %q requires repair. The candidate catalog omits its provider. The saved connection remains intact.", id))
			}
		}
	}
	return a, nil
}

func (p *publishedSetup) assessCandidate(_ context.Context) error {
	a, err := assessUpdateConfiguration(p.home)
	if err != nil {
		return err
	}
	p.candidateAssessment = a
	return nil
}

func (p *publishedSetup) verifyCandidateConfiguration() error {
	a, err := assessUpdateConfiguration(p.home)
	if err != nil {
		return err
	}
	if p.candidateAssessment == nil || p.candidateAssessment.digest != a.digest || p.candidateAssessment.present != a.present {
		return errors.New("The configuration changed after update confirmation. Run install.sh again to review the changes.")
	}
	return nil
}

// Project checks use temporary volumes and mount no user data.
func (p *publishedSetup) checkCandidateProjectImages(ctx context.Context) error {
	database, err := p.database(ctx)
	if err != nil {
		return err
	}
	if len(database.ProjectImages) == 0 {
		return nil
	}
	available := map[string]bool{}
	for _, selection := range database.ProjectImages {
		for _, required := range []string{selection.Image, selection.Base} {
			if required == "" || available[required] {
				continue
			}
			if _, err := p.docker(ctx, "image", "inspect", required); err != nil {
				return fmt.Errorf("Project %q requires an absent image. Restore its selected image before the update.", selection.ProjectID)
			}
			available[required] = true
		}
	}
	root := ""
	if p.update != nil {
		root = filepath.Join(deployment.UpdateDirectory(p.home, p.update.ID), "project-image-check")
	} else {
		root, err = os.MkdirTemp("", "culler-runtime-check-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(root)
	}
	liveNames, err := deployment.RuntimeNames(p.home)
	if err != nil {
		return err
	}
	names, err := deployment.RuntimeNames(root)
	if err != nil {
		return err
	}
	volume := names.RuntimeVolume
	optionalVolume, err := deployment.OpenCodeVolume(root)
	if err != nil {
		return err
	}
	container := "culler-update-check-" + names.Suffix
	if output, err := p.docker(ctx, "container", "inspect", "--format", "{{json .}}", container); err == nil {
		var previous struct {
			Config struct{ Labels map[string]string }
		}
		if json.Unmarshal(output, &previous) != nil || previous.Config.Labels["mc-managed"] != "true" || previous.Config.Labels["mc-deployment"] != liveNames.Suffix {
			return errors.New("The candidate check container belongs to another deployment.")
		}
		if _, err := p.docker(ctx, "rm", "--force", container); err != nil {
			return errors.New("The previous candidate check container could not close. Retry the update.")
		}
	} else if !dockerObjectMissing(output) {
		return errors.New("The previous candidate check container could not be checked.")
	}
	if err := p.clearCandidateCheckVolume(ctx, volume, "update-check", names.Suffix); err != nil {
		return err
	}
	if err := p.clearCandidateCheckVolume(ctx, optionalVolume, "opencode", names.Suffix); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = p.docker(cleanup, "rm", "--force", container)
		_, _ = p.docker(cleanup, "volume", "rm", optionalVolume)
		_, _ = p.docker(cleanup, "volume", "rm", volume)
	}()
	if _, err := p.docker(ctx, "volume", "create", "--label", "mc-component=update-check", "--label", "mc-deployment="+names.Suffix, volume); err != nil {
		return err
	}
	if output, err := p.docker(ctx, "run", "--rm", "--name", container, "--label", "mc-managed=true", "--label", "mc-deployment="+liveNames.Suffix, "--network", "none", "--user", "0:0", "--entrypoint", "/bin/sh", "--mount", "type=volume,src="+volume+",dst=/runtime-check,volume-nocopy", p.record.Image, "-ec", "cp -a /opt/culler-runtime/. /runtime-check/"); err != nil {
		return fmt.Errorf("The candidate runtime check could not start: %s", strings.TrimSpace(string(output)))
	}
	checkedOptional, err := p.candidateCheckOpenCode(ctx, root)
	if err != nil {
		return err
	}
	checked := map[string]bool{}
	for _, selection := range database.ProjectImages {
		if checked[selection.Image] {
			continue
		}
		args := []string{"run", "--rm", "--name", container, "--label", "mc-managed=true", "--label", "mc-deployment=" + liveNames.Suffix, "--network", "none", "--cap-drop", "ALL", "--user", "10002:10002", "--read-only", "--security-opt", "no-new-privileges=true", "--pids-limit", "128", "--memory", "512m", "--cpus", "1", "--entrypoint", "/bin/sh", "--tmpfs", "/tmp:rw,nosuid,nodev,mode=1777", "--mount", "type=volume,src=" + volume + ",dst=" + deployment.RuntimeRoot + ",readonly,volume-nocopy"}
		if checkedOptional != "" {
			args = append(args, "--mount", "type=volume,src="+optionalVolume+",dst="+deployment.OpenCodeRoot+",readonly,volume-nocopy")
		}
		args = append(args, selection.Image, "-ec", candidateRuntimeScript(checkedOptional != ""), "check", p.record.Commit)
		checkContext, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_, checkErr := p.docker(checkContext, args...)
		cancel()
		if checkErr != nil {
			return fmt.Errorf("Project %q cannot run the candidate runtime. The installed release remains intact. Repair the selected Project image, then retry.", selection.ProjectID)
		}
		checked[selection.Image] = true
	}
	return nil
}

func candidateRuntimeScript(openCode bool) string {
	if !openCode {
		return candidateRuntimeCheck
	}
	return candidateRuntimeCheck + "XDG_DATA_HOME=/tmp/data XDG_CONFIG_HOME=/tmp/config XDG_CACHE_HOME=/tmp/cache XDG_STATE_HOME=/tmp/state /opt/culler-opencode/node_modules/@opencode/cli-linux-arm64/bin/opencode --version\n"
}

func (p *publishedSetup) clearCandidateCheckVolume(ctx context.Context, volume, component, owner string) error {
	output, err := p.docker(ctx, "volume", "inspect", "--format", "{{json .}}", volume)
	if err != nil {
		if dockerObjectMissing(output) {
			return nil
		}
		return errors.New("The previous candidate check volume could not be checked.")
	}
	var previous struct {
		Name, Driver    string
		Options, Labels map[string]string
	}
	if json.Unmarshal(output, &previous) != nil || previous.Name != volume || previous.Driver != "local" || len(previous.Options) != 0 || previous.Labels["mc-component"] != component || previous.Labels["mc-deployment"] != owner {
		return errors.New("The candidate check volume belongs to another deployment.")
	}
	if _, err := p.docker(ctx, "volume", "rm", volume); err != nil {
		return errors.New("The previous candidate check volume could not close. Retry the update.")
	}
	return nil
}

func (p *publishedSetup) candidateCheckOpenCode(ctx context.Context, scratch string) (string, error) {
	volume, err := deployment.OpenCodeVolume(p.home)
	if err != nil {
		return "", err
	}
	output, err := p.docker(ctx, "volume", "inspect", "--format", "{{json .}}", volume)
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "no such volume") {
			return "", nil
		}
		return "", errors.New("The OpenCode volume could not be checked.")
	}
	names, _ := deployment.RuntimeNames(p.home)
	var owner struct {
		Name, Driver    string
		Options, Labels map[string]string
	}
	if json.Unmarshal(output, &owner) != nil || owner.Name != volume || owner.Driver != "local" || len(owner.Options) != 0 || owner.Labels["mc-component"] != "opencode" || owner.Labels["mc-deployment"] != names.Suffix {
		return "", errors.New("The OpenCode volume has a different owner or configuration.")
	}
	if p.record.OpenCode == nil || p.update == nil {
		return "", errors.New("The candidate lacks the required OpenCode archive.")
	}
	archive := filepath.Join(deployment.UpdateDirectory(p.home, p.update.ID), p.record.OpenCode.Name)
	scratchNames, _ := deployment.RuntimeNames(scratch)
	err = deployment.InstallOpenCodeArchive(ctx, scratch, p.record.Image, archive, p.record.OpenCode.SHA256, p.record.Commit,
		func(ctx context.Context, _ string, args ...string) ([]byte, error) {
			if len(args) > 0 && args[0] == "run" {
				args = append([]string{"run", "--name", "culler-update-check-" + scratchNames.Suffix, "--label", "mc-managed=true", "--label", "mc-deployment=" + names.Suffix}, args[1:]...)
			}
			return p.docker(ctx, args...)
		})
	if err != nil {
		return "", err
	}
	return deployment.OpenCodeVolume(scratch)
}
