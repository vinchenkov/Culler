package verbs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"mc/boundary"
	"mc/repository"
)

// changeDataPath is the path of the change data of one merge candidate step
// inside the artifact plane of the Project.
func changeDataPath(task int64, run string) string {
	return filepath.Join(strconv.FormatInt(task, 10), "changes", run)
}

// validChangeDataDirectory reports whether a container directory holds the
// change data of the task and run in one artifact plane.
func validChangeDataDirectory(directory string, task int64, run string) bool {
	rest, inRoot := strings.CutPrefix(directory, ArtifactsMountRoot()+"/")
	plane, ofRun := strings.CutSuffix(rest, "/"+changeDataPath(task, run))
	return inRoot && ofRun && filepath.Clean(directory) == directory && boundary.ValidateTarget(plane) == nil
}

// projectArtifactPlane returns the artifact plane of the Project: the target
// of the mount allowlist entry that holds the Project artifact root, and the
// path of the artifact root inside that entry. An agent container mounts the
// artifact root at the same plane.
func projectArtifactPlane(roots map[string]string, workspaceRoot string) (string, string, error) {
	artifactRoot := ProjectArtifactRoot(workspaceRoot)
	canonical, err := filepath.EvalSymlinks(artifactRoot)
	if err != nil {
		return "", "", Domainf("the Project artifact root %s is unreadable: %v", artifactRoot, err)
	}
	var target, inside string
	matches := 0
	for name, path := range roots {
		entry, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(entry, canonical)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		if relative == "." {
			relative = ""
		}
		target, inside = name, relative
		matches++
	}
	if matches != 1 {
		return "", "", Domainf("the mount allowlist must hold the Project artifact root %s in exactly one entry, and %d entries hold it", artifactRoot, matches)
	}
	return target, inside, nil
}

// changeDataName is the file name of the change data of a merged candidate,
// without the extension.
const changeDataName = "candidate"

// storeChangeData derives the change data of a merged candidate from the
// Project repository and stores it in the artifact plane of the Project.
func storeChangeData(ctx context.Context, g repository.Git, roots map[string]string, workspaceRoot string,
	task int64, run string, c repository.Candidate) (*repository.CandidateChanges, error) {
	target, inside, err := projectArtifactPlane(roots, workspaceRoot)
	if err != nil {
		return nil, err
	}
	relative := filepath.Join(inside, changeDataPath(task, run))
	directory := filepath.Join(ArtifactsMountRoot(), target, relative)
	if !validChangeDataDirectory(directory, task, run) {
		return nil, Domainf("the mount allowlist target %q cannot name an artifact plane", target)
	}
	root, err := os.OpenRoot(roots[target])
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := root.MkdirAll(relative, 0755); err != nil {
		return nil, err
	}
	comparison, patch, err := g.Compare(ctx, c.DestinationCommit, c.Commit)
	if err != nil {
		return nil, err
	}
	inventory, err := json.Marshal(comparison)
	if err != nil {
		return nil, err
	}
	if err := writeReadOnly(root, filepath.Join(relative, changeDataName+".patch"), patch); err != nil {
		return nil, err
	}
	if err := writeReadOnly(root, filepath.Join(relative, changeDataName+".json"), inventory); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(inventory)
	return &repository.CandidateChanges{Candidate: repository.ChangeRecord{
		FromCommit: c.DestinationCommit, ToCommit: c.Commit,
		InventoryPath: filepath.Join(directory, changeDataName+".json"), InventorySHA256: hex.EncodeToString(sum[:]),
		PatchPath: filepath.Join(directory, changeDataName+".patch"), PatchSHA256: comparison.PatchSHA256,
		PatchBytes: comparison.PatchBytes, Files: len(comparison.Files),
	}}, nil
}

// writeReadOnly replaces one file. The name shows the complete bytes only
// after they reach storage.
func writeReadOnly(root *os.Root, name string, body []byte) error {
	token, err := newRunID()
	if err != nil {
		return err
	}
	temporary := filepath.Join(filepath.Dir(name), ".change-"+token)
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(0444); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	directory, err := root.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	err = directory.Sync()
	directory.Close()
	return err
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validateChangeData accepts the change data record of a merged candidate
// only for its exact commits, inside the directory of the candidate step.
// The host selects the artifact plane from the mount allowlist.
func validateChangeData(changes *repository.CandidateChanges, task int64, run string, c repository.Candidate) error {
	if changes == nil {
		return Domainf("the merged candidate requires its change data")
	}
	r := changes.Candidate
	directory := filepath.Dir(r.InventoryPath)
	if !validChangeDataDirectory(directory, task, run) {
		return Domainf("the change data requires the directory of its task and run")
	}
	if !repository.ValidCommit(r.FromCommit) || r.FromCommit != c.DestinationCommit || r.ToCommit != c.Commit {
		return Domainf("the change data does not identify its exact commits")
	}
	if r.InventoryPath != filepath.Join(directory, changeDataName+".json") || r.PatchPath != filepath.Join(directory, changeDataName+".patch") {
		return Domainf("the change data is outside the directory of its candidate step")
	}
	if !sha256Hex.MatchString(r.InventorySHA256) || !sha256Hex.MatchString(r.PatchSHA256) || r.PatchBytes < 0 || r.Files < 0 {
		return Domainf("the change data requires exact digests and sizes")
	}
	return nil
}
