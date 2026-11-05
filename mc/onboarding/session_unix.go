//go:build darwin || linux

package onboarding

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"mc/boundary"

	"golang.org/x/sys/unix"
)

const sessionName = "session.json"

// LoadSession returns an empty record when setup has no saved choices.
func LoadSession(home string) (Session, error) {
	dir, err := openSessionDir(home, false)
	if errors.Is(err, os.ErrNotExist) {
		return Session{}, nil
	}
	if err != nil {
		return Session{}, err
	}
	defer dir.Close()
	fd, err := unix.Openat(int(dir.Fd()), sessionName, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return Session{}, nil
	}
	if err != nil {
		return Session{}, fmt.Errorf("open the setup record: %w", err)
	}
	path := filepath.Join(dir.Name(), sessionName)
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	if err := checkSessionFile(fd, path); err != nil {
		return Session{}, err
	}
	body, err := io.ReadAll(io.LimitReader(file, maxSessionBytes+1))
	if err != nil {
		return Session{}, fmt.Errorf("read the setup record: %w", err)
	}
	return decodeSession(body)
}

// SaveSession replaces the record after its complete contents reach the filesystem.
func SaveSession(home string, session Session) error {
	body, err := encodeSession(session)
	if err != nil {
		return err
	}
	dir, err := openSessionDir(home, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	dirFD := int(dir.Fd())
	if err := inspectSessionTarget(dirFD, dir.Name()); err != nil {
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("prepare the setup record: %w", err)
	}
	temporary := ".session-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(dirFD, temporary, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("create the setup record: %w", err)
	}
	defer func() { _ = unix.Unlinkat(dirFD, temporary, 0) }()
	path := filepath.Join(dir.Name(), temporary)
	file := os.NewFile(uintptr(fd), path)
	if err := checkSessionFile(fd, path); err != nil {
		_ = file.Close()
		return err
	}
	_, writeErr := file.Write(body)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("save the setup record: %w", err)
	}
	if err := inspectSessionTarget(dirFD, dir.Name()); err != nil {
		return err
	}
	if err := unix.Renameat(dirFD, temporary, dirFD, sessionName); err != nil {
		return fmt.Errorf("replace the setup record: %w", err)
	}
	if err := dir.Sync(); err != nil {
		return fmt.Errorf("sync the setup directory: %w", err)
	}
	return nil
}

func inspectSessionTarget(dirFD int, dir string) error {
	fd, err := unix.Openat(dirFD, sessionName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect the setup record: %w", err)
	}
	defer unix.Close(fd)
	return checkSessionFile(fd, filepath.Join(dir, sessionName))
}

func checkSessionFile(fd int, path string) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect the setup record: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 ||
		stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return fmt.Errorf("the setup record must be an owner-only regular file with one link")
	}
	return trustSessionPath(fd, path, false)
}

func trustSessionPath(fd int, path string, directory bool) error {
	if err := matchSessionPath(fd, path); err != nil {
		return err
	}
	var err error
	if directory {
		err = boundary.TrustHomeDir(path, os.Geteuid())
	} else {
		err = boundary.TrustPolicyFile(path, os.Geteuid())
	}
	if err != nil {
		return fmt.Errorf("the setup record requires owner-only access: %w", err)
	}
	return matchSessionPath(fd, path)
}

func matchSessionPath(fd int, path string) error {
	var opened, named unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil {
		return fmt.Errorf("inspect the open setup path: %w", err)
	}
	if err := unix.Lstat(path, &named); err != nil {
		return fmt.Errorf("inspect the saved setup path: %w", err)
	}
	if opened.Dev != named.Dev || opened.Ino != named.Ino || opened.Mode&unix.S_IFMT != named.Mode&unix.S_IFMT {
		return fmt.Errorf("the setup path changed during its access check")
	}
	return nil
}

func openSessionDir(home string, create bool) (*os.File, error) {
	if !filepath.IsAbs(home) || filepath.Clean(home) != home || filepath.Dir(home) == home {
		return nil, fmt.Errorf("the setup home must be an absolute directory path")
	}
	parent := filepath.Dir(home)
	suffix := []string{filepath.Base(home)}
	for {
		if _, err := os.Lstat(parent); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect the setup home: %w", err)
		} else if !create {
			return nil, os.ErrNotExist
		}
		suffix = append(suffix, filepath.Base(parent))
		parent = filepath.Dir(parent)
	}
	// Resolve system aliases before descriptor-relative access to the setup directories.
	canonical, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return nil, fmt.Errorf("resolve the setup parent directory: %w", err)
	}
	fd, err := unix.Open(canonical, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open the setup parent directory: %w", err)
	}
	for index := len(suffix) - 1; index >= 0; index-- {
		next, nextErr := openSetupDirectory(fd, suffix[index], create, false)
		_ = unix.Close(fd)
		if nextErr != nil {
			return nil, nextErr
		}
		fd = next
		canonical = filepath.Join(canonical, suffix[index])
	}
	setupFD, err := openSetupDirectory(fd, "setup", create, true)
	_ = unix.Close(fd)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(canonical, "setup")
	if err := trustSessionPath(setupFD, path, true); err != nil {
		_ = unix.Close(setupFD)
		return nil, err
	}
	return os.NewFile(uintptr(setupFD), path), nil
}

func openSetupDirectory(parentFD int, name string, create, private bool) (int, error) {
	if create {
		if err := unix.Mkdirat(parentFD, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, fmt.Errorf("create the setup directory: %w", err)
		}
	}
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("open the setup directory: %w", err)
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("inspect the setup directory: %w", err)
	}
	unsafeMode := stat.Mode&0o022 != 0 || (private && stat.Mode&0o077 != 0)
	if stat.Uid != uint32(os.Geteuid()) || unsafeMode {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("the setup directory must belong to the operator and exclude access from other users")
	}
	return fd, nil
}
