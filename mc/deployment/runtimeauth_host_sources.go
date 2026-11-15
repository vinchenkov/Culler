package deployment

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// NativeCredentialSourceRoot contains the temporary native credential directories.
func NativeCredentialSourceRoot() string {
	temporary, err := filepath.EvalSymlinks(os.TempDir())
	if err != nil {
		temporary = os.TempDir()
	}
	return filepath.Join(temporary, fmt.Sprintf("culler-host-auth-%d", os.Getuid()))
}

type runtimeAuthSourceBoundary struct {
	fd   int
	path string
	flow []string
}

func retainRuntimeAuthSourceBoundary(flow string) (*runtimeAuthSourceBoundary, error) {
	root := NativeCredentialSourceRoot()
	relative, err := filepath.Rel(root, flow)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(relative, string(os.PathSeparator))
	if len(parts) != 3 || len(parts[0]) != 64 || parts[1] != "sources" || !strings.HasPrefix(parts[2], ".provider-login-") {
		return nil, fmt.Errorf("The native credential flow is outside its protected directory.")
	}
	if _, err := hex.DecodeString(parts[0]); err != nil {
		return nil, err
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if _, _, err := runtimeAuthDirectoryIdentity(fd, "host credential source directory"); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return &runtimeAuthSourceBoundary{fd: fd, path: root, flow: parts}, nil
}

// A replaced ancestor can redirect a native program after the login starts.
// Remove only that flow's fixed credential files from the replacement path.
func (b *runtimeAuthSourceBoundary) removeCredentialFiles() error {
	oldErr := b.removeCredentialFilesAt(b.fd)
	current, err := unix.Open(b.path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if os.IsNotExist(err) {
		return oldErr
	}
	if err != nil {
		return errors.Join(oldErr, err)
	}
	defer unix.Close(current)
	dev, ino, err := runtimeAuthDirectoryIdentity(current, "host credential source directory")
	if err != nil {
		return errors.Join(oldErr, err)
	}
	oldDev, oldIno, err := runtimeAuthRetainedDirectoryIdentity(b.fd, "host credential source directory")
	if err != nil {
		return errors.Join(oldErr, err)
	}
	if dev == oldDev && ino == oldIno {
		return oldErr
	}
	return errors.Join(oldErr, b.removeCredentialFilesAt(current))
}

func (b *runtimeAuthSourceBoundary) removeCredentialFilesAt(root int) error {
	paths := [][]string{
		{"chatgpt", "codex", "auth.json"},
		{"claude", "config", ".credentials.json"},
		{"claude", "home", "Library", "Keychains", "culler.keychain-db"},
		{"claude", "home", "Library", "Keychains", "culler.keychain-db-wal"},
		{"claude", "home", "Library", "Keychains", "culler.keychain-db-shm"},
	}
	var failures []error
	for _, path := range paths {
		failures = append(failures, removeRuntimeAuthCredentialAt(root, append(append([]string{}, b.flow...), path...)))
	}
	return errors.Join(failures...)
}

func removeRuntimeAuthCredentialAt(root int, path []string) error {
	fd, err := unix.Openat(root, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { unix.Close(fd) }()
	for _, name := range path[:len(path)-1] {
		next, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, _, err := runtimeAuthDirectoryIdentity(next, "replacement credential directory"); err != nil {
			unix.Close(next)
			return err
		}
		unix.Close(fd)
		fd = next
	}
	name := path[len(path)-1]
	var info unix.Stat_t
	if err := unix.Fstatat(fd, name, &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG || int(info.Uid) != os.Getuid() || info.Nlink != 1 {
		return fmt.Errorf("The replacement credential file must be a singly linked file owned by the operator.")
	}
	if err := unix.Unlinkat(fd, name, 0); err != nil {
		return err
	}
	return unix.Fsync(fd)
}

func runtimeAuthSourceRoot(home string) string {
	canonical, err := CanonicalHome(home)
	if err != nil {
		canonical = filepath.Clean(home)
	}
	sum := sha256.Sum256([]byte(canonical))
	return filepath.Join(NativeCredentialSourceRoot(), hex.EncodeToString(sum[:]), "sources")
}

func prepareRuntimeAuthSourceRoot(home string) (string, error) {
	root := runtimeAuthSourceRoot(home)
	canonical, err := CanonicalHome(home)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(canonical, root)
	if err != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) {
		return "", fmt.Errorf("The temporary credential directory must stay outside the deployment home.")
	}
	for _, path := range []string{NativeCredentialSourceRoot(), filepath.Dir(root), root} {
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
		if err := requireOwnerOnlyDirectory(path, "host credential source directory"); err != nil {
			return "", err
		}
	}
	return root, nil
}
