package wiki

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	operatorconfig "mc/config"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"mc/repository"
)

// ExportFiles are the files that the export command writes into one export
// folder.
var ExportFiles = []string{"client-lib.js", "client.js", "graph.json", "index.html", "styles.css"}

const (
	ExportRecordName  = "export.json"
	ExportFailureName = "failure.json"
	ExportTimeout     = 120 * time.Second
	// ExportRetryDelay is the time before the export command runs again for
	// a commit and openwiki/ tree that failed.
	ExportRetryDelay = 10 * time.Minute

	ExportCurrent  = "current"
	ExportExported = "exported"
	ExportAbsent   = "absent"
	ExportFailed   = "failed"

	exportStagePrefix     = ".stage-"
	exportLockName        = ".lock"
	exportReadTimeout     = 60 * time.Second
	maxExportRecordBytes  = 64 << 10
	maxExportFiles        = 10000
	maxExportErrorBytes   = 512
	maxExportStderrBytes  = 8 << 10
	exportContainerPrefix = "mc-wiki-export-"
)

// ExportRecord is the export.json file of one Project. It names the accepted
// commit and the openwiki/ tree of the export, and the folder that holds it.
type ExportRecord struct {
	Project    string `json:"project"`
	Commit     string `json:"commit"`
	Tree       string `json:"tree"`
	Directory  string `json:"directory"`
	ExportedAt string `json:"exported_at"`
}

// ExportFailure is the failure.json file of one Project. It names the commit
// and the openwiki/ tree of the last failed export, its time, and its cause.
type ExportFailure struct {
	Project  string `json:"project"`
	Commit   string `json:"commit"`
	Tree     string `json:"tree"`
	FailedAt string `json:"failed_at"`
	Error    string `json:"error"`
}

type ExportProject struct {
	ID string
	// Root is the repository root, or an empty string when the Project has
	// no repository.
	Root string
}

type ExportResult struct {
	Project string  `json:"project"`
	Status  string  `json:"status"`
	Commit  *string `json:"commit"`
	Error   string  `json:"error,omitempty"`
}

// ExportContainer is one run of the export command. Input is the private
// copy of the openwiki/ tree, and Output is the empty folder that receives
// the export.
type ExportContainer struct {
	Deployment string
	Name       string
	Image      string
	Project    string
	Input      string
	Output     string
}

type ExportRunner func(ctx context.Context, container ExportContainer) error

// Exporter keeps one wiki export for each Project below Root.
type Exporter struct {
	Root  string
	Image string
	Run   ExportRunner
	Now   func() time.Time
}

// Export brings the wiki export of each Project to its accepted commit and
// removes the exports of other Projects. A Project failure keeps the previous
// export of that Project. The error reports a failure of the export root.
func (e Exporter) Export(ctx context.Context, projects []ExportProject) ([]ExportResult, error) {
	if err := privateDirectory(e.Root); err != nil {
		return nil, err
	}
	unlock, err := lockExports(e.Root)
	if err != nil {
		return nil, err
	}
	defer unlock()
	listed := map[string]bool{}
	folded := map[string]int{}
	for _, project := range projects {
		listed[project.ID] = true
		folded[strings.ToLower(project.ID)]++
	}
	entries, err := os.ReadDir(e.Root)
	if err != nil {
		return nil, fmt.Errorf("read the wiki export folder: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == exportLockName || (listed[entry.Name()] && validExportName(entry.Name())) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(e.Root, entry.Name())); err != nil {
			return nil, fmt.Errorf("remove the wiki export of a removed Project: %w", err)
		}
	}
	// A Project whose last export failed goes last, so that a slow failure
	// delays the other Projects less.
	order := make([]int, 0, len(projects))
	var failedBefore []int
	for i, project := range projects {
		if _, failed := ReadExportFailure(e.Root, project.ID); failed {
			failedBefore = append(failedBefore, i)
		} else {
			order = append(order, i)
		}
	}
	results := make([]ExportResult, len(projects))
	for _, i := range append(order, failedBefore...) {
		project := projects[i]
		switch {
		case !validExportName(project.ID):
			results[i] = failedExport(project.ID, "", "the Project ID is not a valid folder name")
		case folded[strings.ToLower(project.ID)] > 1:
			results[i] = failedExport(project.ID, "", "another Project ID differs from this ID only in letter case")
		default:
			results[i] = e.exportProject(ctx, project)
		}
	}
	return results, nil
}

func (e Exporter) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e Exporter) exportProject(ctx context.Context, project ExportProject) ExportResult {
	dir := filepath.Join(e.Root, project.ID)
	if !filepath.IsAbs(project.Root) || filepath.Clean(project.Root) == "/" {
		if err := os.RemoveAll(dir); err != nil {
			return failedExport(project.ID, "", fmt.Sprintf("remove the wiki export: %v", err))
		}
		return ExportResult{Project: project.ID, Status: ExportAbsent}
	}
	readCtx, cancel := context.WithTimeout(ctx, exportReadTimeout)
	defer cancel()
	git := repository.Git{Root: project.Root}
	commit, err := git.Branch(readCtx, "culler")
	if err != nil {
		return failedExport(project.ID, "", fmt.Sprintf("read the accepted commit: %v", err))
	}
	tree, err := git.TreeID(readCtx, commit, "openwiki")
	if err != nil {
		return failedExport(project.ID, commit, fmt.Sprintf("read the openwiki tree: %v", err))
	}
	if tree == "" {
		if err := os.RemoveAll(dir); err != nil {
			return failedExport(project.ID, commit, fmt.Sprintf("remove the wiki export: %v", err))
		}
		return ExportResult{Project: project.ID, Status: ExportAbsent, Commit: &commit}
	}
	if record, ok := ReadExport(e.Root, project.ID); ok && record.Commit == commit && record.Tree == tree {
		_ = pruneExport(dir, record.Directory)
		return ExportResult{Project: project.ID, Status: ExportCurrent, Commit: &commit}
	}
	if failure, ok := ReadExportFailure(e.Root, project.ID); ok && failure.Commit == commit && failure.Tree == tree {
		failedAt, _ := time.Parse(time.RFC3339, failure.FailedAt)
		if now := e.now(); !now.Before(failedAt) && now.Sub(failedAt) < ExportRetryDelay {
			return failedExport(project.ID, commit, failure.Error)
		}
	}
	if err := e.replaceExport(ctx, readCtx, git, project.ID, dir, commit, tree); err != nil {
		result := failedExport(project.ID, commit, err.Error())
		// A stop request is not a failure of this commit and tree.
		if ctx.Err() == nil {
			_ = writeExportJSON(dir, ExportFailureName, ExportFailure{Project: project.ID, Commit: commit, Tree: tree,
				FailedAt: e.now().UTC().Format(time.RFC3339), Error: result.Error})
		}
		return result
	}
	return ExportResult{Project: project.ID, Status: ExportExported, Commit: &commit}
}

func (e Exporter) replaceExport(ctx, readCtx context.Context, git repository.Git, project, dir, commit, tree string) error {
	if err := privateDirectory(dir); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(dir, exportStagePrefix)
	if err != nil {
		return fmt.Errorf("create the export stage: %w", err)
	}
	defer os.RemoveAll(stage)
	input := filepath.Join(stage, "input", "openwiki")
	if err := materializeWiki(readCtx, git, commit, input); err != nil {
		return err
	}
	output := filepath.Join(stage, "output")
	if err := os.Mkdir(output, 0o700); err != nil {
		return fmt.Errorf("create the export output: %w", err)
	}
	// The container user owns no host file, so it writes only while the
	// output folder admits every user.
	if err := os.Chmod(output, 0o777); err != nil {
		return fmt.Errorf("open the export output: %w", err)
	}
	name, err := exportContainerName()
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, ExportTimeout)
	defer cancel()
	runErr := e.Run(runCtx, ExportContainer{Name: name, Image: e.Image, Project: project, Input: input, Output: output})
	if err := os.Chmod(output, 0o700); err != nil && runErr == nil {
		runErr = err
	}
	if runErr != nil {
		return fmt.Errorf("the export command failed: %w", runErr)
	}
	if err := checkExportOutput(output); err != nil {
		return err
	}
	target := filepath.Join(dir, commit)
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("replace the export folder: %w", err)
	}
	if err := os.Rename(output, target); err != nil {
		return fmt.Errorf("move the export folder: %w", err)
	}
	record := ExportRecord{Project: project, Commit: commit, Tree: tree, Directory: commit,
		ExportedAt: e.now().UTC().Format(time.RFC3339)}
	if err := writeExportJSON(dir, ExportRecordName, record); err != nil {
		return fmt.Errorf("write the export record: %w", err)
	}
	_ = pruneExport(dir, commit)
	return nil
}

// ReadExport returns the export record of one Project when the record is
// valid and its folder holds every export file.
func ReadExport(root, project string) (ExportRecord, bool) {
	if !validExportName(project) {
		return ExportRecord{}, false
	}
	dir := filepath.Join(root, project)
	var record ExportRecord
	if !readExportJSON(filepath.Join(dir, ExportRecordName), &record) {
		return ExportRecord{}, false
	}
	if record.Project != project || !repository.ValidCommit(record.Commit) ||
		!repository.ValidCommit(record.Tree) || record.Directory != record.Commit {
		return ExportRecord{}, false
	}
	if _, err := time.Parse(time.RFC3339, record.ExportedAt); err != nil {
		return ExportRecord{}, false
	}
	folder := filepath.Join(dir, record.Directory)
	if info, err := os.Lstat(folder); err != nil || !info.IsDir() {
		return ExportRecord{}, false
	}
	for _, name := range ExportFiles {
		if info, err := os.Lstat(filepath.Join(folder, name)); err != nil || !info.Mode().IsRegular() {
			return ExportRecord{}, false
		}
	}
	return record, true
}

// ReadExportFailure returns the failure record of one Project when the record
// is valid.
func ReadExportFailure(root, project string) (ExportFailure, bool) {
	var failure ExportFailure
	if !validExportName(project) || !readExportJSON(filepath.Join(root, project, ExportFailureName), &failure) {
		return ExportFailure{}, false
	}
	if failure.Project != project || !repository.ValidCommit(failure.Commit) || !repository.ValidCommit(failure.Tree) {
		return ExportFailure{}, false
	}
	if _, err := time.Parse(time.RFC3339, failure.FailedAt); err != nil {
		return ExportFailure{}, false
	}
	return failure, true
}

// readExportJSON decodes one small regular JSON file with known fields only.
func readExportJSON(path string, value any) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxExportRecordBytes {
		return false
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		return false
	}
	_, err = decoder.Token()
	return err == io.EOF
}

// ReadExports returns the valid export record of each Project below root.
func ReadExports(root string) map[string]ExportRecord {
	records := map[string]ExportRecord{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return records
	}
	for _, entry := range entries {
		if record, ok := ReadExport(root, entry.Name()); ok {
			records[entry.Name()] = record
		}
	}
	return records
}

// materializeWiki writes the regular files of the openwiki/ tree of one
// commit. It reads Git objects only, so checkout files cannot enter.
func materializeWiki(ctx context.Context, git repository.Git, commit, root string) error {
	entries, err := git.Tree(ctx, commit, "openwiki")
	if err != nil {
		return fmt.Errorf("read the openwiki tree: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return fmt.Errorf("create the export input: %w", err)
	}
	var total int64
	count := 0
	for _, entry := range entries {
		name, ok := strings.CutPrefix(entry.Path, "openwiki/")
		if !ok || !filepath.IsLocal(filepath.FromSlash(name)) {
			continue
		}
		// The export command reads regular files only. Links and
		// submodules stay outside the input.
		if entry.Mode != "100644" && entry.Mode != "100755" {
			continue
		}
		count++
		total += entry.Size
		if total > maxWikiBytes || count > maxExportFiles {
			return fmt.Errorf("the openwiki tree exceeds the export limit of 64 MiB or %d files", maxExportFiles)
		}
		body, err := git.ReadTreeFile(ctx, entry, maxWikiBytes)
		if err != nil {
			return fmt.Errorf("read the openwiki tree: %w", err)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("create the export input: %w", err)
		}
		file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("write the export input %s: %w", entry.Path, err)
		}
		_, writeErr := file.Write(body)
		if closeErr := file.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			return fmt.Errorf("write the export input %s: %w", entry.Path, writeErr)
		}
	}
	// The container user reads the input through its own user ID.
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if entry.IsDir() {
			mode = 0o755
		}
		return os.Chmod(path, mode)
	})
}

func checkExportOutput(output string) error {
	entries, err := os.ReadDir(output)
	if err != nil {
		return fmt.Errorf("read the export output: %w", err)
	}
	found := map[string]bool{}
	for _, entry := range entries {
		found[entry.Name()] = true
	}
	for _, name := range ExportFiles {
		if !found[name] {
			return fmt.Errorf("the export command did not write %s", name)
		}
		delete(found, name)
	}
	if len(found) != 0 {
		extra := make([]string, 0, len(found))
		for name := range found {
			extra = append(extra, name)
		}
		sort.Strings(extra)
		return fmt.Errorf("the export command wrote the unexpected file %s", extra[0])
	}
	for _, name := range ExportFiles {
		path := filepath.Join(output, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("the export file %s is not a regular file", name)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("protect the export file %s: %w", name, err)
		}
	}
	return nil
}

func writeExportJSON(dir, name string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, exportStagePrefix+"record-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, writeErr := temp.Write(append(body, '\n'))
	if writeErr == nil {
		writeErr = temp.Sync()
	}
	if closeErr := temp.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return writeErr
	}
	return os.Rename(temp.Name(), filepath.Join(dir, name))
}

// pruneExport removes each entry of a Project folder except the export record
// and the folder that it names. It also removes the failure record.
func pruneExport(dir, keep string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var first error
	for _, entry := range entries {
		if entry.Name() == ExportRecordName || entry.Name() == keep {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func privateDirectory(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create the wiki export folder: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspect the wiki export folder: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("the wiki export folder %s is not a directory", dir)
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("protect the wiki export folder: %w", err)
		}
	}
	return nil
}

func lockExports(root string) (func(), error) {
	file, err := os.OpenFile(filepath.Join(root, exportLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the wiki export lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("another wiki export is in progress")
		}
		return nil, fmt.Errorf("lock the wiki exports: %w", err)
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}

func validExportName(name string) bool {
	return name != "" && name[0] != '.' && filepath.Base(name) == name &&
		!strings.ContainsAny(name, "/\\\x00")
}

func failedExport(project, commit, text string) ExportResult {
	if len(text) > maxExportErrorBytes {
		text = strings.ToValidUTF8(text[:maxExportErrorBytes], "")
	}
	result := ExportResult{Project: project, Status: ExportFailed, Error: text}
	if commit != "" {
		result.Commit = &commit
	}
	return result
}

func exportContainerName() (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("name the export container: %w", err)
	}
	return exportContainerPrefix + hex.EncodeToString(random[:]), nil
}

// ExportDockerArgs is the docker command line of one export run. The
// container has no network, no credential, and no environment variable.
func ExportDockerArgs(c ExportContainer) []string {
	return []string{
		"run", "--rm", "--name", c.Name, "--pull", "never",
		"--network", "none", "--user", "10002:10002", "--cap-drop", "ALL",
		"--security-opt", "no-new-privileges=true", "--read-only",
		operatorconfig.PidsLimitFlag,
		"--label", "mc-managed=true", "--label", "mc-deployment=" + c.Deployment, "--label", "mc-tier=wiki-export", "--label", "mc-project=" + c.Project,
		"-v", c.Input + ":/input/openwiki:ro", "-v", c.Output + ":/output",
		c.Image,
		"/usr/bin/env", "-i", "PATH=/usr/local/bin:/usr/bin:/bin",
		"/usr/local/bin/node", "/opt/openwiki/node_modules/openwiki/dist/cli/cli.js",
		"visualize", "/input/openwiki", "--export", "/output",
	}
}

// DockerExport runs the export command in the runner image. A run that fails
// or passes its deadline stops and removes its container.
func DockerExport(ctx context.Context, c ExportContainer) error {
	if strings.Contains(c.Input, ":") || strings.Contains(c.Output, ":") {
		return fmt.Errorf("the export paths must not contain a colon")
	}
	cmd := exec.CommandContext(ctx, "docker", ExportDockerArgs(c)...)
	cmd.Stdout = io.Discard
	stderr := &headBuffer{limit: maxExportStderrBytes}
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if err == nil {
		return nil
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	remove := exec.CommandContext(cleanup, "docker", "rm", "-f", c.Name)
	remove.Stdout, remove.Stderr = io.Discard, io.Discard
	_ = remove.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("the container did not finish within %d seconds", int(ExportTimeout/time.Second))
	}
	if detail := stderr.firstLine(); detail != "" {
		return fmt.Errorf("%v: %s", err, detail)
	}
	return err
}

// headBuffer keeps the first bytes that a process writes.
type headBuffer struct {
	limit int
	body  []byte
}

func (b *headBuffer) Write(p []byte) (int, error) {
	if room := b.limit - len(b.body); room > 0 {
		b.body = append(b.body, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func (b *headBuffer) firstLine() string {
	for _, line := range strings.Split(string(b.body), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
