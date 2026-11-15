package verbs

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"mc/boundary"
)

type WorkspacePreview struct {
	Path       string          `json:"path"`
	Exists     bool            `json:"exists"`
	Repository bool            `json:"repository"`
	Identity   string          `json:"identity"`
	Vision     WorkspaceVision `json:"vision"`
}

type WorkspaceVision struct {
	Content  string             `json:"content"`
	Revision string             `json:"revision"`
	Check    ProjectVisionCheck `json:"check"`
}

func visionRevision(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

// PreviewWorkspace attests a directory, or its parent when the directory is absent.
func PreviewWorkspace(path string) (WorkspacePreview, error) {
	result := WorkspacePreview{}
	if strings.HasPrefix(path, "/") && strings.TrimRight(path, "/") != "" {
		path = strings.TrimRight(path, "/")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsAny(path, "\x00\r\n,") {
		return result, Usagef("Enter the full path to a project folder on this Mac.")
	}
	target := path
	info, err := os.Lstat(path)
	exists := err == nil
	if errors.Is(err, os.ErrNotExist) {
		target = filepath.Dir(path)
	} else if err != nil {
		return result, Domainf("The project folder is unavailable: %v", err)
	}
	identity, err := boundary.ResolveSource(target)
	if err == nil && exists && !identity.IsDir {
		return result, Domainf("This path points to a file. Choose the folder that contains your Project.")
	}
	if err != nil || !identity.IsDir {
		return result, Domainf("The project folder or its parent must be an accessible directory.")
	}
	canonical := identity.Canonical
	if !exists {
		canonical = filepath.Join(canonical, filepath.Base(path))
	}
	if (boundary.BlockPolicy{}).Rejects(path, canonical) {
		return result, Domainf("This folder cannot be a project workspace.")
	}
	if exists && info.Mode()&os.ModeSymlink != 0 {
		return result, Domainf("Use the project's actual folder instead of a symbolic link.")
	}
	if exists {
		info, err = os.Stat(canonical)
	} else {
		info, err = os.Stat(identity.Canonical)
	}
	if err != nil {
		return result, Domainf("The folder identity is unavailable.")
	}
	result = WorkspacePreview{Path: canonical, Exists: exists, Identity: workspaceDirectoryIdentity(info, exists)}
	if exists {
		if err := ValidateOnboardWorkspaceRoots([]OnboardWorkspaceRoot{{ID: "preview", Root: canonical}}); err != nil {
			return result, err
		}
		git, gitErr := os.Lstat(filepath.Join(canonical, ".git"))
		if gitErr != nil && !errors.Is(gitErr, os.ErrNotExist) {
			return result, Domainf("The Git repository is unavailable.")
		}
		result.Repository = gitErr == nil
		if result.Repository && (!git.IsDir() || git.Mode()&os.ModeSymlink != 0) {
			return result, Domainf("Select the main project folder. A linked Git worktree cannot be a Project workspace.")
		}
		result.Repository, err = inspectOnboardGitRoot(canonical)
		if err != nil {
			return result, err
		}
		result.Vision, err = ReadWorkspaceVision(canonical)
		if err != nil {
			return result, err
		}
	} else {
		parentRepository, err := inspectOnboardGitRoot(identity.Canonical)
		if err != nil {
			return result, err
		}
		if parentRepository {
			return result, Domainf("Select the repository root, or create the new project outside this repository.")
		}
		result.Vision = WorkspaceVision{Revision: "absent", Check: ProjectVisionResult(canonical, ProjectVisionAbsent)}
	}
	return result, nil
}

func ReadWorkspaceVision(path string) (WorkspaceVision, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return WorkspaceVision{}, Domainf("The project folder is unavailable.")
	}
	defer root.Close()
	return readWorkspaceVision(root, path)
}

func readWorkspaceVision(root *os.Root, path string) (WorkspaceVision, error) {
	result := WorkspaceVision{Check: ProjectVisionResult(path, ProjectVisionAbsent)}
	info, err := root.Lstat(ProjectVisionFile)
	if errors.Is(err, os.ErrNotExist) {
		result.Revision = "absent"
		return result, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o444 == 0 {
		return result, Domainf("VISION.md must be a readable regular file inside the project folder.")
	}
	file, err := root.OpenFile(ProjectVisionFile, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return result, Domainf("VISION.md is unreadable.")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return result, Domainf("VISION.md changed. Reload the vision before another save.")
	}
	body, err := io.ReadAll(io.LimitReader(file, maxProjectVisionBytes+1))
	if err != nil || len(body) > maxProjectVisionBytes || !utf8.Valid(body) {
		return result, Domainf("VISION.md must contain valid text of at most 64 KiB.")
	}
	result.Content, result.Revision = string(body), visionRevision(body)
	result.Check = checkProjectVisionContent(path, body)
	return result, nil
}

// SaveWorkspaceVision rejects a stale editor and replaces the file atomically.
func SaveWorkspaceVision(path, revision, content, directoryIdentity string) (WorkspaceVision, error) {
	if len(content) > maxProjectVisionBytes || !utf8.ValidString(content) || strings.TrimSpace(content) == "" {
		return WorkspaceVision{}, Usagef("Enter a project goal of at most 64 KiB.")
	}
	pathInfo, err := os.Lstat(path)
	if err != nil || !pathInfo.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 {
		return WorkspaceVision{}, Domainf("The project folder changed. Open Project details before another save.")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return WorkspaceVision{}, err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil || directoryIdentity == "" || workspaceDirectoryIdentity(info, true) != directoryIdentity {
		return WorkspaceVision{}, Domainf("The project folder changed. Reload Project details before another save.")
	}
	current, err := readWorkspaceVision(root, path)
	if err != nil {
		return current, err
	}
	if current.Content == content && current.Revision != "absent" {
		return current, nil
	}
	if revision == "" || current.Revision != revision {
		return current, Domainf("VISION.md changed outside this form. Reload the saved vision and compare it with your text.")
	}
	name := ".vision-" + hex.EncodeToString([]byte(rand.Text()))
	temp, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return current, err
	}
	defer root.Remove(name)
	mode := os.FileMode(0o644)
	if info, err := root.Lstat(ProjectVisionFile); err == nil {
		mode = info.Mode().Perm()
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return current, err
	}
	_, err = temp.WriteString(content)
	if err == nil {
		err = temp.Sync()
	}
	err = errors.Join(err, temp.Close())
	if err != nil {
		return current, err
	}
	latest, err := readWorkspaceVision(root, path)
	if err != nil {
		return current, err
	}
	if latest.Revision != revision {
		return current, Domainf("VISION.md changed. Reload the saved vision before another save.")
	}
	if revision == "absent" {
		err = root.Link(name, ProjectVisionFile)
	} else {
		err = root.Rename(name, ProjectVisionFile)
	}
	if err != nil {
		return current, Domainf("The vision save failed: %v", err)
	}
	directory, err := root.Open(".")
	if err != nil {
		return current, err
	}
	err = errors.Join(directory.Sync(), directory.Close())
	if err != nil {
		return current, err
	}
	return readWorkspaceVision(root, path)
}
