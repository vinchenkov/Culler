package deployment

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"mc/distribution"
	"mc/opencodecatalog"
)

const OpenCodeRoot = "/opt/culler-opencode"
const openCodeOperation = "opencode-download"

// OpenCodeRefusalMessage names the other catalog entries that take the same key.
func OpenCodeRefusalMessage(entry string, err error) string {
	code, _ := agentProfileOperationError("model_test", err)
	if code == "authorization" {
		if provider, ok := opencodecatalog.Find(entry); ok {
			return provider.RefusedKey()
		}
	}
	return ""
}

func OpenCodeVolume(home string) (string, error) {
	names, err := RuntimeNames(home)
	return "mc-opencode-" + names.Suffix, err
}

func OpenCodeDownloadStatus(home string) *AgentProfileOperation {
	root := filepath.Join(home, "agent-profile-operations")
	record, err := readAgentProfileOperation(root, openCodeOperation)
	if err != nil {
		return nil
	}
	if !agentProfileTerminal(record.Status) {
		if current, err := GetAgentProfileOperation(home, openCodeOperation); err == nil {
			return &current
		}
	}
	return &record.AgentProfileOperation
}

func StartOpenCodeDownload(home string, retry bool) (AgentProfileOperation, error) {
	return startOpenCodeDownload(home, retry, defaultAgentProfileLaunch)
}

func startOpenCodeDownload(home string, retry bool, launch func(string, string) error) (AgentProfileOperation, error) {
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return AgentProfileOperation{}, err
	}
	lock, err := agentProfileOperationLock(root, openCodeOperation)
	if err != nil {
		return AgentProfileOperation{}, err
	}
	defer agentProfileUnlock(lock)
	if current, err := readAgentProfileOperation(root, openCodeOperation); err == nil {
		if !retry || current.Status == "pending" || current.Status == "running" {
			return current.AgentProfileOperation, nil
		}
	}
	record := agentProfileOperationRecord{AgentProfileOperation: AgentProfileOperation{
		OperationID: openCodeOperation, Kind: "opencode_download", Status: "pending", Message: "OpenCode waits for the download.", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)},
		Request: AgentProfileOperationRequest{OperationID: openCodeOperation, Kind: "opencode_download"}}
	if err := writeAgentProfileOperation(root, record); err != nil {
		return record.AgentProfileOperation, err
	}
	if err := launch(home, openCodeOperation); err != nil {
		record.Status = "failed"
		record.Message = "The host could not start the OpenCode download. Retry the download."
		_ = writeAgentProfileOperation(root, record)
	}
	return record.AgentProfileOperation, nil
}

func runOpenCodeDownload(home, image string) error {
	return runOpenCodeDownloadWith(home, func(ctx context.Context, progress func(string)) error {
		return downloadOpenCode(ctx, home, image, progress)
	})
}

func runOpenCodeDownloadWith(home string, download func(context.Context, func(string)) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return err
	}
	lock, err := agentProfileOperationLock(root, openCodeOperation+".worker")
	if err != nil {
		return err
	}
	defer agentProfileUnlock(lock)
	record, err := readAgentProfileOperation(root, openCodeOperation)
	if err != nil {
		return err
	}
	if record.Status != "pending" {
		return nil
	}
	progress := func(message string) {
		_, _ = updateAgentProfileOperation(home, openCodeOperation, func(record *agentProfileOperationRecord) error {
			record.Status = "running"
			record.Message = message
			return nil
		})
	}
	progress("Read the OpenCode release record.")
	err = download(ctx, progress)
	_, saveErr := updateAgentProfileOperation(home, openCodeOperation, func(record *agentProfileOperationRecord) error {
		record.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		record.Status = "succeeded"
		record.Message = "OpenCode is ready."
		record.ErrorCode = ""
		if err != nil {
			record.Status = "failed"
			record.ErrorCode = "opencode_download"
			var failure *openCodeSourceError
			if errors.As(err, &failure) {
				record.ErrorCode = "opencode_" + failure.Stage
			}
			record.Message = err.Error()
		}
		return nil
	})
	return saveErr
}

func downloadOpenCode(ctx context.Context, home, image string, progress func(string)) error {
	return downloadOpenCodeWithDocker(ctx, home, image, progress, runRuntimeDocker)
}

func downloadOpenCodeWithDocker(ctx context.Context, home, image string, progress func(string), run RuntimeDocker) error {
	if update, err := LoadUpdateJournal(home); err != nil {
		return err
	} else if update != nil && update.BlocksWork() && update.Phase != "restore" {
		if update.Candidate.OpenCode == nil {
			return fmt.Errorf("The candidate lacks the required OpenCode archive.")
		}
		archive := filepath.Join(UpdateDirectory(home, update.ID), update.Candidate.OpenCode.Name)
		progress("Check the retained OpenCode archive.")
		return InstallOpenCodeArchive(ctx, home, image, archive, update.Candidate.OpenCode.SHA256, update.Candidate.Commit, run)
	}

	if source, err := openCodeSourceForImage(home, image); err != nil {
		return err
	} else if source != nil {
		return buildOpenCodeSource(ctx, home, image, *source, progress, run)
	}
	record, err := distribution.Load(filepath.Join(home, distribution.InstalledRecord))
	if pending, pendingErr := distribution.Load(filepath.Join(home, "state", "pending-release.json")); pendingErr == nil {
		record, err = pending, nil
	}
	if err != nil || record.OpenCode == nil {
		return fmt.Errorf("The installed release lacks its OpenCode archive. Repair the installation, then retry.")
	}
	token, err := distribution.Credential(ctx)
	if err != nil {
		return err
	}
	client := distribution.NewClient("", token)
	client.HTTP.Timeout = 15 * time.Minute
	root, err := os.MkdirTemp("", "culler-opencode-download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	archive := filepath.Join(root, record.OpenCode.Name)
	progress("Download OpenCode and its web search plugin.")
	if err := client.Download(ctx, record, *record.OpenCode, archive); err != nil {
		return fmt.Errorf("The OpenCode download failed: %w", err)
	}
	progress("Unpack the OpenCode archive.")
	return InstallOpenCodeArchive(ctx, home, image, archive, record.OpenCode.SHA256, record.Commit, run)
}

type runtimeSourceRecord struct {
	Source  string `json:"source"`
	Release string `json:"release"`
}

func readRuntimeSource(home string) (runtimeSourceRecord, error) {
	var source runtimeSourceRecord
	body, err := readSecretSource(filepath.Join(home, "state", "runtime-source.json"), "runtime source")
	if err != nil {
		return source, err
	}
	if json.Unmarshal(body, &source) != nil || !filepath.IsAbs(source.Source) || !validProductionBuildID(source.Release) {
		return source, fmt.Errorf("The runtime source is invalid.")
	}
	return source, nil
}

func RuntimeSourceDirectory(home string) (string, error) {
	source, err := readRuntimeSource(home)
	return source.Source, err
}

// ConfigureRuntimeSource retains the source of a local deployment.
func ConfigureRuntimeSource(home, runner, release string) error {
	file := filepath.Join(home, "state", "runtime-source.json")
	root := filepath.Dir(runner)
	if _, err := os.Stat(filepath.Join(root, "release", "opencode", "build.sh")); os.IsNotExist(err) {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := requireOperatorSourceDirectory(root, "runtime source"); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	body, _ := json.Marshal(runtimeSourceRecord{Source: root, Release: release})
	temporary, err := os.CreateTemp(filepath.Dir(file), ".runtime-source-")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err := temporary.Write(body); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), file)
}

// UpdateOpenCodeVolume prepares source packages or updates an installed published package before service activation.
func UpdateOpenCodeVolume(ctx context.Context, home, image, release string) error {
	if err := updateOpenCodeVolume(ctx, home, image, release, runRuntimeDocker); err != nil {
		return err
	}
	// The retained live models belong to the previous release catalog and the previous pinned OpenCode.
	StartOpenCodeModelRefresh(home, true)
	return nil
}

func updateOpenCodeVolume(ctx context.Context, home, image, release string, run RuntimeDocker) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	if source, err := openCodeSourceForImage(home, image); err != nil {
		return err
	} else if source != nil {
		if source.Release != release {
			return fmt.Errorf("The OpenCode source record differs from the selected release.")
		}
		return prepareOpenCodeSource(ctx, home, image, *source, run)
	}
	volume, err := OpenCodeVolume(home)
	if err != nil {
		return err
	}
	output, err := run(ctx, "", "volume", "inspect", volume)
	if err != nil {
		if strings.Contains(strings.ToLower(string(output)), "no such volume") {
			absent, err := clearAbsentSourceOpenCodeStatus(ctx, home, volume, run)
			if err != nil || absent {
				return err
			}
		} else {
			return fmt.Errorf("The host could not inspect the OpenCode volume.")
		}
	}
	current, err := run(ctx, "", "run", "--rm", "--network", "none", "--cap-drop", "ALL", "--user", "10002:10002", "--mount", "type=volume,src="+volume+",dst="+OpenCodeRoot+",readonly,volume-nocopy", image, "cat", OpenCodeRoot+"/release-id")
	if err == nil && strings.TrimSpace(string(current)) == release && !sourceOpenCodeNeedsPublishedArchive(ctx, home, image, volume, release, run) {
		return nil
	}
	// A refresh of the live models reads the volume. The installation waits for that container.
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return err
	}
	worker, err := agentProfileOperationLockContext(ctx, root, openCodeOperation+".worker")
	if err != nil {
		return err
	}
	err = downloadOpenCodeWithDocker(ctx, home, image, func(string) {}, run)
	agentProfileUnlock(worker)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(home, "agent-profile-operations", openCodeOperation+".json")); err == nil {
		_, err = updateAgentProfileOperation(home, openCodeOperation, func(record *agentProfileOperationRecord) error {
			record.Status = "succeeded"
			record.Message = "OpenCode is ready."
			record.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			return nil
		})
		return err
	}
	return nil
}

const openCodeInstallScript = `set -eu
 tar -xzf /archive.tar.gz --no-same-owner -C /output
 chmod -R a+rX /output
 version=$(XDG_DATA_HOME=/tmp/data XDG_CONFIG_HOME=/tmp/config XDG_CACHE_HOME=/tmp/cache XDG_STATE_HOME=/tmp/state /output/node_modules/@opencode/cli-linux-arm64/bin/opencode --version)
 [ "$version" = "opencode v$3" ] || { printf 'The archive contains %s. This release requires OpenCode %s.\n' "$version" "$3" >&2; exit 1; }
 printf '%s\n' "$1" > /output/release-id
 printf '%s\n' "$2" > /output/archive-sha256
 chmod a+r /output/release-id /output/archive-sha256`

// InstallOpenCodeArchive validates the archive before it changes the volume.
func InstallOpenCodeArchive(ctx context.Context, home, image, archive, checksum, release string, run RuntimeDocker) error {
	if run == nil {
		run = runRuntimeDocker
	}
	if image == "" || strings.HasPrefix(image, "-") || !validSHA256(checksum) || release == "" {
		return fmt.Errorf("The OpenCode release identity is invalid.")
	}
	if err := checkOpenCodeArchive(archive, checksum); err != nil {
		return err
	}
	volume, err := OpenCodeVolume(home)
	if err != nil {
		return err
	}
	names, _ := RuntimeNames(home)
	users, err := run(ctx, "", "container", "ls", "--quiet", "--filter", "volume="+volume)
	if err != nil || strings.TrimSpace(string(users)) != "" {
		return fmt.Errorf("Stop the OpenCode agents before the OpenCode update.")
	}
	inspect, inspectErr := run(ctx, "", "volume", "inspect", "--format", "{{json .}}", volume)
	if inspectErr == nil {
		var record struct {
			Name, Driver    string
			Options, Labels map[string]string
		}
		if json.Unmarshal(inspect, &record) != nil || record.Name != volume || record.Driver != "local" || len(record.Options) != 0 || record.Labels["mc-component"] != "opencode" || record.Labels["mc-deployment"] != names.Suffix {
			return fmt.Errorf("The OpenCode volume belongs to another deployment.")
		}
		if _, err := run(ctx, "", "volume", "rm", volume); err != nil {
			return fmt.Errorf("The previous OpenCode volume could not be removed.")
		}
	} else if !strings.Contains(strings.ToLower(string(inspect)), "no such volume") {
		return fmt.Errorf("The OpenCode volume could not be checked.")
	}
	if _, err := run(ctx, "", "volume", "create", "--label", "mc-component=opencode", "--label", "mc-deployment="+names.Suffix, volume); err != nil {
		return err
	}
	usable := false
	defer func() {
		if !usable {
			_, _ = run(context.WithoutCancel(ctx), "", "volume", "rm", volume)
		}
	}()
	output, err := run(ctx, "", "run", "--rm", "--network", "none", "--user", "0:0", "--mount", "type=volume,src="+volume+",dst=/output,volume-nocopy", "--mount", "type=bind,src="+archive+",dst=/archive.tar.gz,readonly", image, "/bin/sh", "-ec", openCodeInstallScript, "install", release, checksum, opencodecatalog.Release().OpenCodeVersion)
	if err != nil {
		return fmt.Errorf("The OpenCode installation failed: %s", lastLine(output))
	}
	usable = true
	return nil
}

func checkOpenCodeArchive(filename, checksum string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != checksum {
		return fmt.Errorf("The OpenCode archive checksum differs from the release record. Retry the download.")
	}
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	files := map[string]bool{}
	kinds := map[string]byte{}
	links := map[string]string{}
	programs := 0
	var total int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("The OpenCode archive is incomplete.")
		}
		name := path.Clean(header.Name)
		if name == "." && header.Typeflag == tar.TypeDir {
			continue
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || files[name] || header.Mode&07000 != 0 {
			return fmt.Errorf("The OpenCode archive contains an unsafe path.")
		}
		files[name] = true
		kinds[name] = header.Typeflag
		switch header.Typeflag {
		case tar.TypeDir:
		case tar.TypeSymlink:
			target := path.Clean(path.Join(path.Dir(name), header.Linkname))
			if path.IsAbs(header.Linkname) || target == ".." || strings.HasPrefix(target, "../") {
				return fmt.Errorf("The OpenCode archive contains an unsafe link.")
			}
			links[name] = target
		case tar.TypeReg:
			total += header.Size
			if total > 2<<30 {
				return fmt.Errorf("The OpenCode archive exceeds its size limit.")
			}
			if strings.HasSuffix(name, "/bin/opencode") {
				programs++
				if name != "node_modules/@opencode/cli-linux-arm64/bin/opencode" {
					return fmt.Errorf("The OpenCode archive contains another platform.")
				}
				var elf [20]byte
				if _, err := io.ReadFull(reader, elf[:]); err != nil || string(elf[:4]) != "\x7fELF" || elf[4] != 2 || elf[5] != 1 || elf[18] != 0xb7 || elf[19] != 0 {
					return fmt.Errorf("The OpenCode program requires Linux arm64.")
				}
			}
		default:
			return fmt.Errorf("The OpenCode archive contains an unsupported file.")
		}
	}
	if _, err := io.Copy(io.Discard, compressed); err != nil {
		return fmt.Errorf("The OpenCode archive is incomplete.")
	}
	for name := range files {
		for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
			if kind, exists := kinds[parent]; exists && kind != tar.TypeDir {
				return fmt.Errorf("The OpenCode archive contains an unsafe parent.")
			}
		}
	}
	for _, target := range links {
		if kinds[target] != tar.TypeReg {
			return fmt.Errorf("The OpenCode archive link must name a regular file.")
		}
	}
	if programs != 1 || !files["control/shell"] || !files["control/server.mjs"] || !files["node_modules/opencode-websearch/dist/index.js"] {
		return fmt.Errorf("The OpenCode archive lacks required files.")
	}
	return nil
}
