package deployment

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// runtimeAuthDisposableRoot retains the directory descriptors that created
// credential-bearing scratch state. Cleanup empties only those retained
// inodes; path replacement cannot redirect recursive deletion.
type runtimeAuthDisposableRoot struct {
	homeFD    int
	parentFD  int
	rootFD    int
	name      string
	homeDev   uint64
	homeIno   uint64
	parentDev uint64
	parentIno uint64
	dev       uint64
	ino       uint64
}

func runtimeAuthDirectoryIdentity(fd int, label string) (uint64, uint64, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return 0, 0, fmt.Errorf("inspect %s descriptor: %w", label, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o777 != 0o700 ||
		int(stat.Uid) != os.Getuid() {
		return 0, 0, fmt.Errorf("%s descriptor must be an owner-only directory with mode 0700", label)
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}

func runtimeAuthRetainedDirectoryIdentity(fd int, label string) (uint64, uint64, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return 0, 0, fmt.Errorf("inspect %s descriptor: %w", label, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || int(stat.Uid) != os.Getuid() {
		return 0, 0, fmt.Errorf("%s retained descriptor is not an operator-owned directory", label)
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}

func openRuntimeAuthDisposableRoot(
	sourceRoot, rootPath, prefix, label string,
) (*runtimeAuthDisposableRoot, error) {
	name := filepath.Base(rootPath)
	if filepath.Dir(rootPath) != sourceRoot || !strings.HasPrefix(name, prefix) {
		return nil, fmt.Errorf("refuse unsafe %s target %q", label, rootPath)
	}
	home := filepath.Dir(sourceRoot)
	homeFD, err := unix.Open(
		home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s credential directory: %w", label, err)
	}
	homeDev, homeIno, err := runtimeAuthDirectoryIdentity(homeFD, label+" credential directory")
	if err != nil {
		unix.Close(homeFD)
		return nil, err
	}
	parentFD, err := unix.Open(
		sourceRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		unix.Close(homeFD)
		return nil, fmt.Errorf("open %s parent: %w", label, err)
	}
	parentDev, parentIno, err := runtimeAuthDirectoryIdentity(parentFD, label+" parent")
	if err != nil {
		unix.Close(parentFD)
		unix.Close(homeFD)
		return nil, err
	}
	rootFD, err := unix.Openat(
		parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		unix.Close(parentFD)
		unix.Close(homeFD)
		return nil, fmt.Errorf("open %s: %w", label, err)
	}
	dev, ino, err := runtimeAuthDirectoryIdentity(rootFD, label)
	if err != nil {
		unix.Close(rootFD)
		unix.Close(parentFD)
		unix.Close(homeFD)
		return nil, err
	}
	return &runtimeAuthDisposableRoot{
		homeFD: homeFD, parentFD: parentFD, rootFD: rootFD, name: name,
		homeDev: homeDev, homeIno: homeIno,
		parentDev: parentDev, parentIno: parentIno, dev: dev, ino: ino,
	}, nil
}

func runtimeAuthPathDirectoryIdentity(path, label string) (uint64, uint64, error) {
	fd, err := unix.Open(
		path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, 0, fmt.Errorf("open current %s: %w", label, err)
	}
	dev, ino, identityErr := runtimeAuthDirectoryIdentity(fd, label)
	closeErr := unix.Close(fd)
	return dev, ino, errors.Join(identityErr, closeErr)
}

func (d *runtimeAuthDisposableRoot) requireCurrentParent(path, label string) error {
	homeDev, homeIno, err := runtimeAuthPathDirectoryIdentity(
		filepath.Dir(path), label+" credential directory")
	if err != nil {
		return err
	}
	if homeDev != d.homeDev || homeIno != d.homeIno {
		return fmt.Errorf("%s credential directory path no longer names the retained credential directory", label)
	}
	dev, ino, err := runtimeAuthPathDirectoryIdentity(path, label+" parent")
	if err != nil {
		return err
	}
	if dev != d.parentDev || ino != d.parentIno {
		return fmt.Errorf("%s parent path no longer names the retained source root", label)
	}
	return nil
}

func (d *runtimeAuthDisposableRoot) expectedNameIdentity() (uint64, uint64, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(
		d.parentFD, d.name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return 0, 0, err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return 0, 0, fmt.Errorf("runtime-auth disposable expected path is not a directory")
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}

func (d *runtimeAuthDisposableRoot) requireExpectedPath(sourceRoot, label string) error {
	if err := d.requireCurrentParent(sourceRoot, label); err != nil {
		return err
	}
	dev, ino, err := d.expectedNameIdentity()
	if err != nil {
		return fmt.Errorf("%s path no longer names its retained identity: %w", label, err)
	}
	if dev != d.dev || ino != d.ino {
		return fmt.Errorf("%s path no longer names its retained identity", label)
	}
	return nil
}

func (d *runtimeAuthDisposableRoot) requireLinkedWithoutReplacement(
	sourceRoot, label string,
) error {
	if err := d.requireCurrentParent(sourceRoot, label); err != nil {
		return err
	}
	name, err := d.linkedName()
	if err != nil {
		return err
	}
	if name == d.name {
		return nil
	}
	if _, _, err := d.expectedNameIdentity(); err == nil {
		return fmt.Errorf("%s original path was replaced while its retained identity moved", label)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect %s original path: %w", label, err)
	}
	return nil
}

func runtimeAuthDirectoryEntries(fd int, label string) ([]os.DirEntry, error) {
	dup, err := unix.Openat(
		fd, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("duplicate %s descriptor: %w", label, err)
	}
	dir := os.NewFile(uintptr(dup), label)
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	return entries, errors.Join(readErr, closeErr)
}

func readRuntimeAuthSecretAt(
	rootFD int,
	directories []string,
	name, label string,
) ([]byte, error) {
	currentFD, err := unix.Openat(
		rootFD, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("duplicate %s root descriptor: %w", label, err)
	}
	defer func() { unix.Close(currentFD) }()
	for _, directory := range directories {
		nextFD, err := unix.Openat(
			currentFD, directory,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, fmt.Errorf("open %s directory %q: %w", label, directory, err)
		}
		if _, _, err := runtimeAuthDirectoryIdentity(nextFD, label+" directory"); err != nil {
			unix.Close(nextFD)
			return nil, err
		}
		unix.Close(currentFD)
		currentFD = nextFD
	}
	var before unix.Stat_t
	if err := unix.Fstatat(currentFD, name, &before, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, fmt.Errorf("inspect %s source: %w", label, err)
	}
	if before.Mode&unix.S_IFMT != unix.S_IFREG || before.Mode&0o777 != 0o600 ||
		int(before.Uid) != os.Getuid() || uint64(before.Nlink) != 1 ||
		before.Size <= 0 || before.Size > maxRuntimeAuthSourceBytes {
		return nil, fmt.Errorf("%s source must be a non-empty, singly linked, operator-owned mode-0600 regular file", label)
	}
	fileFD, err := unix.Openat(
		currentFD, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s source: %w", label, err)
	}
	file := os.NewFile(uintptr(fileFD), label)
	var opened unix.Stat_t
	if err := unix.Fstat(fileFD, &opened); err != nil ||
		uint64(opened.Dev) != uint64(before.Dev) ||
		uint64(opened.Ino) != uint64(before.Ino) {
		file.Close()
		return nil, fmt.Errorf("%s source changed while opening", label)
	}
	body, readErr := io.ReadAll(io.LimitReader(file, maxRuntimeAuthSourceBytes+1))
	var after unix.Stat_t
	statErr := unix.Fstat(fileFD, &after)
	closeErr := file.Close()
	if readErr != nil || statErr != nil || closeErr != nil ||
		len(body) > maxRuntimeAuthSourceBytes ||
		uint64(after.Dev) != uint64(before.Dev) ||
		uint64(after.Ino) != uint64(before.Ino) ||
		after.Size != before.Size ||
		after.Mode&unix.S_IFMT != unix.S_IFREG || after.Mode&0o777 != 0o600 ||
		int(after.Uid) != os.Getuid() || uint64(after.Nlink) != 1 {
		return nil, fmt.Errorf("%s source changed during bounded read", label)
	}
	return body, nil
}

func clearRuntimeAuthDirectoryFD(fd int) error {
	if err := unix.Fchmod(fd, 0o700); err != nil {
		return err
	}
	entries, err := runtimeAuthDirectoryEntries(fd, "runtime-auth disposable root")
	if err != nil {
		return err
	}
	var cleanupErrs []error
	for _, entry := range entries {
		name := entry.Name()
		var stat unix.Stat_t
		if err := unix.Fstatat(fd, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			cleanupErrs = append(cleanupErrs, err)
			continue
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			childFD, err := unix.Openat(
				fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				cleanupErrs = append(cleanupErrs, err)
				continue
			}
			childErr := clearRuntimeAuthDirectoryFD(childFD)
			closeErr := unix.Close(childFD)
			if childErr != nil || closeErr != nil {
				cleanupErrs = append(cleanupErrs, childErr, closeErr)
				continue
			}
			if err := unix.Unlinkat(fd, name, unix.AT_REMOVEDIR); err != nil {
				cleanupErrs = append(cleanupErrs, err)
			}
			continue
		}
		if err := unix.Unlinkat(fd, name, 0); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	return errors.Join(cleanupErrs...)
}

func (d *runtimeAuthDisposableRoot) linkedName() (string, error) {
	entries, err := runtimeAuthDirectoryEntries(d.parentFD, "runtime-auth source root")
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		var stat unix.Stat_t
		if err := unix.Fstatat(
			d.parentFD, entry.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		if stat.Mode&unix.S_IFMT == unix.S_IFDIR &&
			uint64(stat.Dev) == d.dev && uint64(stat.Ino) == d.ino {
			return entry.Name(), nil
		}
	}
	return "", fmt.Errorf("runtime-auth disposable identity is no longer linked below its source root")
}

func (d *runtimeAuthDisposableRoot) Cleanup(label string) error {
	if d == nil || d.rootFD < 0 || d.parentFD < 0 || d.homeFD < 0 {
		return nil
	}
	var cleanupErrs []error
	dev, ino, identityErr := runtimeAuthRetainedDirectoryIdentity(d.rootFD, label)
	if identityErr != nil {
		cleanupErrs = append(cleanupErrs,
			fmt.Errorf("%s retained identity changed: %w", label, identityErr))
	} else if dev != d.dev || ino != d.ino {
		cleanupErrs = append(cleanupErrs,
			fmt.Errorf("%s retained identity changed", label))
	} else {
		if err := clearRuntimeAuthDirectoryFD(d.rootFD); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("empty %s: %w", label, err))
		}
		name, err := d.linkedName()
		if err != nil {
			cleanupErrs = append(cleanupErrs, err)
		} else {
			if name != d.name {
				if _, _, err := d.expectedNameIdentity(); err == nil {
					cleanupErrs = append(cleanupErrs,
						fmt.Errorf("%s original path was replaced while its retained identity moved", label))
				} else if !os.IsNotExist(err) {
					cleanupErrs = append(cleanupErrs,
						fmt.Errorf("inspect %s original path: %w", label, err))
				}
			}
			if err := unix.Unlinkat(d.parentFD, name, unix.AT_REMOVEDIR); err != nil {
				cleanupErrs = append(cleanupErrs, fmt.Errorf("remove %s: %w", label, err))
			}
		}
		if err := unix.Fsync(d.parentFD); err != nil {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("sync %s parent: %w", label, err))
		}
	}
	if err := unix.Close(d.rootFD); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("close %s descriptor: %w", label, err))
	}
	if err := unix.Close(d.parentFD); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("close %s parent descriptor: %w", label, err))
	}
	if err := unix.Close(d.homeFD); err != nil {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("close %s credential directory descriptor: %w", label, err))
	}
	d.rootFD, d.parentFD, d.homeFD = -1, -1, -1
	return errors.Join(cleanupErrs...)
}
