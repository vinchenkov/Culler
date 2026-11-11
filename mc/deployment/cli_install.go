package deployment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const CLIInstallRecord = "state/culler-command.json"

type CLIShellEdit struct {
	Path      string `json:"path"`
	Block     string `json:"block"`
	Directory string `json:"directory,omitempty"`
}
type CLIExecutable struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type CLIInstallation struct {
	Path     string          `json:"path"`
	SHA256   string          `json:"sha256"`
	Release  string          `json:"release"`
	Shells   []CLIShellEdit  `json:"shells"`
	Verified bool            `json:"verified"`
	Previous []CLIExecutable `json:"previous,omitempty"`
}

func CLIFileDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || links != 1 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0111 == 0 {
		return "", errors.New("The Culler command requires an executable file that the operator owns.")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ReadCLIInstallation(home string) (CLIInstallation, error) {
	var r CLIInstallation
	path := filepath.Join(home, CLIInstallRecord)
	info, err := os.Lstat(path)
	if err != nil {
		return r, err
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || links != 1 || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 65536 {
		return r, errors.New("The command record requires an owner-only regular file.")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return r, err
	}
	if json.Unmarshal(body, &r) != nil || !filepath.IsAbs(r.Path) || filepath.Base(r.Path) != "culler" || len(r.SHA256) != 64 {
		return r, errors.New("The Culler command record is invalid.")
	}
	for _, edit := range r.Shells {
		directory := edit.Directory
		if directory == "" {
			directory = filepath.Dir(r.Path)
		}
		if !filepath.IsAbs(edit.Path) || !filepath.IsAbs(directory) || edit.Block != cliShellBlock(home, directory) {
			return r, errors.New("The command record contains an invalid shell change.")
		}
	}
	for _, previous := range r.Previous {
		if !filepath.IsAbs(previous.Path) || filepath.Base(previous.Path) != "culler" || len(previous.SHA256) != 64 {
			return r, errors.New("The command record contains an invalid previous executable.")
		}
	}
	return r, nil
}

func CheckCLIConflict(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := CLIFileDigest(path); err != nil {
		return err
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil || info.Path != "mc/cmd/mc" {
		return fmt.Errorf("Another executable uses %s. Select another directory with install.sh --bin-dir.", path)
	}
	return nil
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func CLIPathCommand(directory string) string {
	return "export PATH=" + shellQuote(directory) + ":\"$PATH\""
}

func CLICommand(home, command string) string {
	userHome, _ := os.UserHomeDir()
	defaultHome, _ := CanonicalHome(filepath.Join(userHome, ".culler"))
	if home == defaultHome && (os.Getenv("MC_HOME") == "" || os.Getenv("MC_HOME") == home) {
		return "culler " + command
	}
	return "culler --mc-home " + shellQuote(home) + " " + command
}

func cliShellBlock(home, directory string) string {
	names, _ := RuntimeNames(home)
	return "\n# Culler command " + names.Suffix + "\ncase \":$PATH:\" in\n  *:" + shellQuote(directory) + ":*) ;;\n  *) " + CLIPathCommand(directory) + " ;;\nesac\n# End Culler command " + names.Suffix + "\n"
}

func writeCLIFile(path string, body []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".culler-command-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err := f.Chmod(mode); err != nil {
		return err
	}
	if _, err := f.Write(body); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func cliShellFile(path string) (string, []byte, os.FileMode, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil, 0644, nil
	}
	if err != nil {
		return path, nil, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return path, nil, 0, err
		}
		info, err = os.Stat(path)
		if err != nil {
			return path, nil, 0, err
		}
	}
	uid, links, ok := statOwner(info)
	if !ok || int(uid) != os.Getuid() || links != 1 || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return path, nil, 0, fmt.Errorf("The shell file %s requires an owner-controlled regular file.", path)
	}
	body, err := os.ReadFile(path)
	return path, body, info.Mode().Perm(), err
}

// InstallCLIPath records exact shell edits so removal preserves other shell text.
func InstallCLIPath(home, userHome, directory, release, parentPath string) (CLIInstallation, error) {
	return installCLIPath(home, userHome, directory, release, parentPath, verifyCLIShell)
}

func installCLIPath(home, userHome, directory, release, parentPath string, verify func(string, string, string, string, string, string) error) (CLIInstallation, error) {
	r := CLIInstallation{Path: filepath.Join(directory, "culler"), Release: release}
	var err error
	r.SHA256, err = CLIFileDigest(r.Path)
	if err != nil {
		return r, err
	}
	if previous, err := ReadCLIInstallation(home); err == nil {
		r.Shells = previous.Shells
		for i := range r.Shells {
			if r.Shells[i].Directory == "" {
				r.Shells[i].Directory = filepath.Dir(previous.Path)
			}
		}
		r.Previous = previous.Previous
		if previous.Path != r.Path {
			r.Previous = append(r.Previous, CLIExecutable{Path: previous.Path, SHA256: previous.SHA256})
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return r, err
	}
	state := filepath.Join(home, "state")
	if err := ensureOwnerDirectory(state, "command state"); err != nil {
		return r, err
	}
	save := func() error {
		body, err := json.Marshal(r)
		if err != nil {
			return err
		}
		return writeCLIFile(filepath.Join(home, CLIInstallRecord), append(body, '\n'), 0600)
	}
	if err := save(); err != nil {
		return r, err
	}
	zdot := os.Getenv("ZDOTDIR")
	if zdot == "" {
		zdot = userHome
	}
	if !filepath.IsAbs(zdot) {
		return r, errors.New("ZDOTDIR requires an absolute path for the Culler command check.")
	}
	bashProfile := filepath.Join(userHome, ".bash_profile")
	for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
		if _, err := os.Lstat(filepath.Join(userHome, name)); err == nil {
			bashProfile = filepath.Join(userHome, name)
			break
		}
	}
	shells := []struct{ shell, mode, path string }{
		{"/bin/zsh", "-ic", filepath.Join(zdot, ".zshrc")},
		{"/bin/zsh", "-lic", filepath.Join(zdot, ".zprofile")},
		{"/bin/bash", "-ic", filepath.Join(userHome, ".bashrc")},
		{"/bin/bash", "-lic", bashProfile},
	}
	block := cliShellBlock(home, directory)
	for _, shell := range shells {
		if verify(shell.shell, shell.mode, r.Path, userHome, zdot, parentPath) == nil {
			continue
		}
		resolved, body, mode, err := cliShellFile(shell.path)
		if err != nil {
			return r, err
		}
		if !bytes.Contains(body, []byte(block)) {
			found := false
			for _, edit := range r.Shells {
				if edit.Path == resolved && edit.Block == block {
					found = true
				}
			}
			if !found {
				r.Shells = append(r.Shells, CLIShellEdit{Path: resolved, Block: block, Directory: directory})
			}
			if err := save(); err != nil {
				return r, err
			}
			if err := writeCLIFile(resolved, append(body, []byte(block)...), mode); err != nil {
				return r, err
			}
		}
	}
	for _, shell := range shells {
		if err := verify(shell.shell, shell.mode, r.Path, userHome, zdot, parentPath); err != nil {
			return r, err
		}
	}
	r.Verified = true
	return r, save()
}

func verifyCLIShell(shell, mode, path, userHome, zdot, parentPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, shell, mode, "printf '\\nCULLER_COMMAND=%s\\n' \"$(command -v culler)\"")
	command.Env = append(os.Environ(), "HOME="+userHome, "PATH="+parentPath, "ZDOTDIR="+zdot)
	body, err := command.Output()
	if err != nil || !bytes.Contains(body, []byte("\nCULLER_COMMAND="+path+"\n")) {
		return fmt.Errorf("A new %s shell cannot resolve culler. Check its shell configuration.", filepath.Base(shell))
	}
	return nil
}

func CLIAvailability(home string) ([]string, error) {
	r, err := ReadCLIInstallation(home)
	if err != nil {
		return nil, err
	}
	digest, err := CLIFileDigest(r.Path)
	if err != nil || digest != r.SHA256 || !r.Verified {
		return nil, errors.New("The Culler command requires an availability check.")
	}
	return []string{"The culler command is installed for lifecycle operations. Continue to the dashboard for the Culler experience."}, nil
}

func RemoveCLIInstallation(home string) error {
	remove, err := PrepareCLIUninstall(home)
	if err != nil {
		return err
	}
	return remove()
}

// PrepareCLIUninstall retains ownership data for command cleanup after directory removal.
func PrepareCLIUninstall(home string) (func() error, error) {
	removeSkill, err := PrepareAgentSkillUninstall(home)
	if err != nil {
		return nil, err
	}
	r, err := ReadCLIInstallation(home)
	if errors.Is(err, os.ErrNotExist) {
		return removeSkill, nil
	}
	if err != nil {
		return nil, err
	}
	return func() error {
		if err := removeSkill(); err != nil {
			return err
		}
		return removeCLIInstallation(home, r)
	}, nil
}

func removeCLIInstallation(home string, r CLIInstallation) error {
	for _, edit := range r.Shells {
		path, body, mode, err := cliShellFile(edit.Path)
		if err != nil {
			return err
		}
		if !bytes.Contains(body, []byte(edit.Block)) {
			continue
		}
		if err := writeCLIFile(path, bytes.ReplaceAll(body, []byte(edit.Block), nil), mode); err != nil {
			return err
		}
	}
	for _, file := range append(r.Previous, CLIExecutable{Path: r.Path, SHA256: r.SHA256}) {
		digest, err := CLIFileDigest(file.Path)
		if err == nil && digest == file.SHA256 {
			if err := os.Remove(file.Path); err != nil {
				return err
			}
		}
	}
	if err := os.Remove(filepath.Join(home, CLIInstallRecord)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
