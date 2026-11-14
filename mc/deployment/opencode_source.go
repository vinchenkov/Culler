package deployment

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	operatorconfig "mc/config"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"

	"mc/distribution"
	"mc/opencodecatalog"
)

//go:embed opencode_source.py
var openCodeSourceProgram string

var openCodeSourceEnvironment = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "TMPDIR=/tmp",
	"npm_config_cache=/tmp/npm-cache", "npm_config_userconfig=/tmp/culler-npmrc", "npm_config_globalconfig=/tmp/culler-global-npmrc"}

type openCodeSourceInputs struct {
	Files    map[string]string `json:"files"`
	Tools    json.RawMessage   `json:"tools"`
	Program  string            `json:"program_sha256"`
	Platform string            `json:"platform"`
	Version  string            `json:"version"`
}

type openCodeSourcePackage struct {
	Inputs   openCodeSourceInputs `json:"inputs"`
	Identity string               `json:"identity"`
	Release  string               `json:"release"`
	Archive  string               `json:"archive_sha256"`
	Content  string               `json:"content_sha256"`
}

type openCodeSourceError struct {
	Stage string
	Cause string
}

func (e *openCodeSourceError) Error() string {
	return fmt.Sprintf("The OpenCode %s from source failed: %s", e.Stage, e.Cause)
}

func openCodeSourceFailure(stage string, err error) error {
	var failure *openCodeSourceError
	if errors.As(err, &failure) {
		return err
	}
	return &openCodeSourceError{Stage: stage, Cause: err.Error()}
}

func openCodeSourceCommandError(stage string, output []byte, err error) error {
	var failure struct{ Stage, Cause string }
	if json.Unmarshal(bytes.TrimSpace(output), &failure) == nil && failure.Cause != "" {
		if failure.Stage == "download" || failure.Stage == "preparation" || failure.Stage == "validation" {
			stage = failure.Stage
		}
		return &openCodeSourceError{Stage: stage, Cause: failure.Cause}
	}
	detail := lastLine(output)
	if detail == "" {
		detail = err.Error()
	}
	return &openCodeSourceError{Stage: stage, Cause: detail}
}

// A published image or active update takes precedence over a retained source record.
func openCodeSourceForImage(home, image string) (*runtimeSourceRecord, error) {
	update, err := LoadUpdateJournal(home)
	if err != nil {
		return nil, err
	}
	if update != nil && update.BlocksWork() {
		return nil, nil
	}
	for _, file := range []string{"state/pending-release.json", distribution.InstalledRecord} {
		if record, err := distribution.Load(filepath.Join(home, file)); err == nil && record.Image == image {
			return nil, nil
		}
	}
	source, err := readRuntimeSource(home)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return &source, err
}

// Equal release commits do not make a source archive equal to a published archive.
func sourceOpenCodeNeedsPublishedArchive(ctx context.Context, home, image, volume, release string, run RuntimeDocker) bool {
	_, packageErr := os.Stat(filepath.Join(home, "state", "opencode-source-package.json"))
	_, sourceErr := os.Stat(filepath.Join(home, "state", "runtime-source.json"))
	if errors.Is(packageErr, os.ErrNotExist) && errors.Is(sourceErr, os.ErrNotExist) {
		return false
	}
	var archive *distribution.Asset
	if update, err := LoadUpdateJournal(home); err == nil && update != nil && update.BlocksWork() && update.Phase != "restore" {
		archive = update.Candidate.OpenCode
	} else {
		for _, file := range []string{"state/pending-release.json", distribution.InstalledRecord} {
			if record, err := distribution.Load(filepath.Join(home, file)); err == nil && record.Image == image && record.Commit == release {
				archive = record.OpenCode
				break
			}
		}
	}
	if archive == nil {
		return true
	}
	output, err := run(ctx, "", "run", "--rm", operatorconfig.PidsLimitFlag, "--network", "none", "--cap-drop", "ALL", "--user", "10002:10002", "--mount",
		"type=volume,src="+volume+",dst="+OpenCodeRoot+",readonly,volume-nocopy", image, "cat", OpenCodeRoot+"/archive-sha256")
	return err != nil || strings.TrimSpace(string(output)) != archive.SHA256
}

func clearAbsentSourceOpenCodeStatus(ctx context.Context, home, volume string, run RuntimeDocker) (bool, error) {
	if _, err := os.Stat(filepath.Join(home, "state", "opencode-source-package.json")); errors.Is(err, os.ErrNotExist) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return false, err
	}
	worker, err := agentProfileOperationLockContext(ctx, root, openCodeOperation+".worker")
	if err != nil {
		return false, err
	}
	defer agentProfileUnlock(worker)
	// A download can complete while this operation waits for the worker.
	output, err := run(ctx, "", "volume", "inspect", volume)
	if err == nil {
		return false, nil
	}
	if !strings.Contains(strings.ToLower(string(output)), "no such volume") {
		return false, fmt.Errorf("The OpenCode volume could not be checked: %s", lastLine(output))
	}
	lock, err := agentProfileOperationLock(root, openCodeOperation)
	if err != nil {
		return false, err
	}
	defer agentProfileUnlock(lock)
	if err := os.Remove(filepath.Join(root, openCodeOperation+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	return true, syncDirectory(root)
}

func prepareOpenCodeSource(ctx context.Context, home, image string, source runtimeSourceRecord, run RuntimeDocker) error {
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return err
	}
	lock, err := agentProfileOperationLockContext(ctx, root, openCodeOperation+".worker")
	if err != nil {
		return err
	}
	defer agentProfileUnlock(lock)
	if err := recordOpenCodeSourceStatus(root, "running", "Check the OpenCode source package.", ""); err != nil {
		return err
	}
	progress := func(message string) {
		_, _ = updateAgentProfileOperation(home, openCodeOperation, func(record *agentProfileOperationRecord) error {
			record.Message = message
			return nil
		})
	}
	err = buildOpenCodeSource(ctx, home, image, source, progress, run)
	status, message, code := "succeeded", "OpenCode is ready.", ""
	if err != nil {
		status, message, code = "failed", err.Error(), "opencode_preparation"
		var failure *openCodeSourceError
		if errors.As(err, &failure) {
			code = "opencode_" + failure.Stage
		}
	}
	return errors.Join(err, recordOpenCodeSourceStatus(root, status, message, code))
}

func recordOpenCodeSourceStatus(root, status, message, code string) error {
	lock, err := agentProfileOperationLock(root, openCodeOperation)
	if err != nil {
		return err
	}
	defer agentProfileUnlock(lock)
	record, _ := readAgentProfileOperation(root, openCodeOperation)
	if status == "running" {
		record = agentProfileOperationRecord{AgentProfileOperation: AgentProfileOperation{
			OperationID: openCodeOperation, Kind: "opencode_download", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)},
			Request: AgentProfileOperationRequest{OperationID: openCodeOperation, Kind: "opencode_download"}}
	} else {
		record.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	record.Status, record.Message, record.ErrorCode = status, message, code
	return writeAgentProfileOperation(root, record)
}

func snapshotOpenCodeSource(ctx context.Context, source runtimeSourceRecord, root string) (map[string]string, error) {
	head, err := exec.CommandContext(ctx, "git", "-C", source.Source, "rev-parse", "HEAD").Output()
	if err != nil || strings.TrimSpace(string(head)) != source.Release {
		return nil, fmt.Errorf("The source checkout differs from the selected release. Run the source deployment again.")
	}
	changed, err := exec.CommandContext(ctx, "git", "-C", source.Source, "status", "--porcelain", "--untracked-files=all", "--", "runner/opencode", "release/opencode").Output()
	if err != nil || len(changed) != 0 {
		return nil, fmt.Errorf("The OpenCode source files differ from the selected commit.")
	}
	// The container receives an immutable copy of the selected commit, without the repository or host credentials.
	archive, err := exec.CommandContext(ctx, "git", "-C", source.Source, "-c", "tar.umask=0022", "archive", source.Release, "runner/opencode", "release/opencode").Output()
	if err != nil {
		return nil, fmt.Errorf("The OpenCode source files could not be read: %w", err)
	}
	files := map[string]string{}
	reader := tar.NewReader(bytes.NewReader(archive))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := path.Clean(header.Name)
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("The OpenCode source contains an unsafe path.")
		}
		file := filepath.Join(root, filepath.FromSlash(name))
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(file, 0755); err != nil {
				return nil, err
			}
			if err := os.Chmod(file, 0755); err != nil {
				return nil, err
			}
			continue
		}
		if header.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("The OpenCode source requires regular files: %s", name)
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		mode := os.FileMode(header.Mode) & 0777
		if err := os.WriteFile(file, body, mode); err != nil {
			return nil, err
		}
		if err := os.Chmod(file, mode); err != nil {
			return nil, err
		}
		digest := sha256.New()
		fmt.Fprintf(digest, "%o\x00", mode)
		digest.Write(body)
		files[name] = hex.EncodeToString(digest.Sum(nil))
	}
	return files, nil
}

func openCodeSourceContainer(image string, args ...string) []string {
	base := []string{"run", "--rm", operatorconfig.PidsLimitFlag, "--pull", "never", "--platform", "linux/arm64", "--cap-drop", "ALL", "--security-opt", "no-new-privileges"}
	base = append(base, args...)
	base = append(base, image, "/usr/bin/env", "-i")
	base = append(base, openCodeSourceEnvironment...)
	return append(base, "python3", "-c", openCodeSourceProgram)
}

func buildOpenCodeSource(ctx context.Context, home, image string, source runtimeSourceRecord, progress func(string), run RuntimeDocker) (result error) {
	defer func() {
		if result != nil {
			result = openCodeSourceFailure("preparation", result)
		}
	}()
	recordFile := filepath.Join(home, "state", "opencode-source-package.json")
	if _, err := os.Lstat(recordFile); errors.Is(err, os.ErrNotExist) {
		// An incomplete record identifies a source attempt but cannot satisfy package verification.
		if err := saveOpenCodeSourcePackage(recordFile, openCodeSourcePackage{Release: source.Release}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	root, err := os.MkdirTemp("", "culler-opencode-source-")
	if err != nil {
		return openCodeSourceFailure("preparation", err)
	}
	defer os.RemoveAll(root)
	snapshot := filepath.Join(root, "source")
	if err := os.Mkdir(snapshot, 0755); err != nil {
		return err
	}
	if err := os.Chmod(snapshot, 0755); err != nil {
		return err
	}
	files, err := snapshotOpenCodeSource(ctx, source, snapshot)
	if err != nil {
		return openCodeSourceFailure("preparation", err)
	}
	identity, err := run(ctx, "", "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil {
		return openCodeSourceCommandError("preparation", identity, err)
	}
	image = strings.TrimSpace(string(identity))
	if !strings.HasPrefix(image, "sha256:") || !validSHA256(strings.TrimPrefix(image, "sha256:")) {
		return fmt.Errorf("The OpenCode preparation image identity is invalid.")
	}
	tools, err := run(ctx, "", append(openCodeSourceContainer(image, "--network", "none", "--user", "10002:10002"), "tools")...)
	if err != nil {
		return openCodeSourceCommandError("preparation", tools, err)
	}
	if !json.Valid(tools) {
		return fmt.Errorf("The OpenCode preparation tools returned an invalid identity.")
	}
	program := sha256.Sum256([]byte(openCodeSourceProgram + openCodeInstallScript + strings.Join(openCodeSourceEnvironment, "\x00")))
	wanted := openCodeSourcePackage{Release: source.Release, Inputs: openCodeSourceInputs{
		Files: files, Tools: json.RawMessage(bytes.TrimSpace(tools)), Program: hex.EncodeToString(program[:]),
		Platform: "linux-arm64", Version: opencodecatalog.Release().OpenCodeVersion}}
	wanted.Identity = agentProfileDigest(wanted.Inputs)
	volume, err := OpenCodeVolume(home)
	if err != nil {
		return err
	}
	exists, err := inspectOpenCodeSourceVolume(ctx, home, volume, run)
	if err != nil {
		return openCodeSourceFailure("validation", err)
	}
	var previous openCodeSourcePackage
	if body, err := readSecretSource(recordFile, "OpenCode source package"); err == nil {
		_ = json.Unmarshal(body, &previous)
	}
	if exists && previous.Identity == wanted.Identity && previous.Identity == agentProfileDigest(previous.Inputs) &&
		validSHA256(previous.Content) && validSHA256(previous.Archive) && validProductionBuildID(previous.Release) {
		progress("Verify the installed OpenCode package.")
		if err := verifyOpenCodeSource(ctx, image, volume, previous, run); err == nil {
			wanted.Content, wanted.Archive = previous.Content, previous.Archive
			if previous.Release != wanted.Release {
				if err := updateOpenCodeSourceRelease(ctx, image, volume, wanted.Release, run); err != nil {
					return err
				}
			}
			return saveOpenCodeSourcePackage(recordFile, wanted)
		}
	}
	progress("Prepare the OpenCode package in the source image.")
	outputRoot := filepath.Join(root, "output")
	if err := os.Mkdir(outputRoot, 0700); err != nil {
		return err
	}
	// The container account can write this mount. The private parent keeps it inaccessible to other host accounts.
	if err := os.Chmod(outputRoot, 0777); err != nil {
		return err
	}
	args := openCodeSourceContainer(image, "--user", "10002:10002",
		"--mount", "type=bind,src="+snapshot+",dst=/source,readonly",
		"--mount", "type=bind,src="+outputRoot+",dst=/output")
	output, err := run(ctx, "", append(args, "build")...)
	if err != nil {
		return openCodeSourceCommandError("preparation", output, err)
	}
	if json.Unmarshal(output, &wanted) != nil || !validSHA256(wanted.Content) || !validSHA256(wanted.Archive) {
		return fmt.Errorf("The OpenCode package preparation returned an invalid archive identity.")
	}
	progress("Install and verify the OpenCode package.")
	archive := filepath.Join(outputRoot, "culler-opencode-linux-arm64.tar.gz")
	if err := InstallOpenCodeArchive(ctx, home, image, archive, wanted.Archive, wanted.Release, run); err != nil {
		return openCodeSourceFailure("installation", err)
	}
	if err := verifyOpenCodeSource(ctx, image, volume, wanted, run); err != nil {
		return err
	}
	return saveOpenCodeSourcePackage(recordFile, wanted)
}

func inspectOpenCodeSourceVolume(ctx context.Context, home, volume string, run RuntimeDocker) (bool, error) {
	output, err := run(ctx, "", "volume", "inspect", "--format", "{{json .}}", volume)
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "no such volume") {
			return false, nil
		}
		return false, fmt.Errorf("The OpenCode volume could not be checked: %s", lastLine(output))
	}
	var record struct {
		Name, Driver    string
		Options, Labels map[string]string
	}
	names, _ := RuntimeNames(home)
	if json.Unmarshal(output, &record) != nil || record.Name != volume || record.Driver != "local" || len(record.Options) != 0 ||
		record.Labels["mc-component"] != "opencode" || record.Labels["mc-deployment"] != names.Suffix {
		return false, fmt.Errorf("The OpenCode volume belongs to another deployment.")
	}
	return true, nil
}

func verifyOpenCodeSource(ctx context.Context, image, volume string, record openCodeSourcePackage, run RuntimeDocker) error {
	args := openCodeSourceContainer(image, "--network", "none", "--user", "10002:10002", "--mount",
		"type=volume,src="+volume+",dst="+OpenCodeRoot+",readonly,volume-nocopy")
	output, err := run(ctx, "", append(args, "verify", OpenCodeRoot, record.Content, record.Archive, record.Release, record.Inputs.Version)...)
	if err != nil {
		return openCodeSourceCommandError("validation", output, err)
	}
	return nil
}

func updateOpenCodeSourceRelease(ctx context.Context, image, volume, release string, run RuntimeDocker) error {
	users, err := run(ctx, "", "container", "ls", "--quiet", "--filter", "volume="+volume)
	if err != nil || strings.TrimSpace(string(users)) != "" {
		return fmt.Errorf("Stop the OpenCode agents before the OpenCode update.")
	}
	output, err := run(ctx, "", "run", "--rm", operatorconfig.PidsLimitFlag, "--network", "none", "--cap-drop", "ALL", "--user", "0:0", "--mount",
		"type=volume,src="+volume+",dst=/output,volume-nocopy", image, "/bin/sh", "-ec",
		`printf '%s\n' "$1" > /output/.release-id; chmod 644 /output/.release-id; mv /output/.release-id /output/release-id`, "release", release)
	if err != nil {
		return openCodeSourceCommandError("installation", output, err)
	}
	return nil
}

func saveOpenCodeSourcePackage(file string, record openCodeSourcePackage) error {
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(file), ".opencode-source-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err = temporary.Write(body); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), file); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(file))
}
