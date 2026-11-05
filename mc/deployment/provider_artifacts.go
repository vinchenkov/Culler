package deployment

import (
	"fmt"
	"io"
	"os"
	"regexp"

	"golang.org/x/sys/unix"
)

var legacyRunCredentialName = regexp.MustCompile(`^(?:[a-f0-9]{16}|h-[a-z0-9-]+-[a-f0-9]{16})\.codex-auth\.json$`)
var legacySetupOperationName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// RetireProviderCredentialArtifacts keeps the host credential before it removes container copies.
func RetireProviderCredentialArtifacts(home string, retireContainers func() error) error {
	if _, err := MigrateProviderCredentialStore(home); err != nil {
		return err
	}
	if err := retireContainers(); err != nil {
		return err
	}
	if err := requireOwnerOnlyDirectory(home, "provider artifact home"); err != nil {
		return err
	}
	if err := scrubPreviousProviderSessions(home); err != nil {
		return err
	}
	fd, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	for _, directory := range []string{"runs", "environment-runs"} {
		child, err := openProviderArtifactDirectory(fd, directory)
		if err == unix.ENOENT {
			continue
		}
		if err != nil {
			return err
		}
		err = removeLegacyProviderArtifacts(child, directory == "environment-runs")
		unix.Close(child)
		if err != nil {
			return err
		}
	}
	return nil
}

func openProviderArtifactDirectory(parent int, name string) (int, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	if _, _, err := runtimeAuthRetainedDirectoryIdentity(fd, "provider artifact directory"); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func removeLegacyProviderArtifacts(fd int, setup bool) error {
	copyFD, err := unix.Dup(fd)
	if err != nil {
		return err
	}
	directory := os.NewFile(uintptr(copyFD), "provider artifact directory")
	defer directory.Close()
	count := 0
	for {
		names, err := directory.Readdirnames(256)
		if err != nil && err != io.EOF {
			return err
		}
		count += len(names)
		if count > 100_000 {
			return fmt.Errorf("The provider artifact directory exceeds its entry limit.")
		}
		for _, name := range names {
			if !setup {
				if legacyRunCredentialName.MatchString(name) {
					if err := removeProviderArtifact(fd, name); err != nil {
						return err
					}
				}
				continue
			}
			if !legacySetupOperationName.MatchString(name) {
				continue
			}
			var info unix.Stat_t
			if err := unix.Fstatat(fd, name, &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				return err
			}
			if info.Mode&unix.S_IFMT == unix.S_IFREG {
				continue
			}
			child, err := openProviderArtifactDirectory(fd, name)
			if err != nil {
				return err
			}
			err = removeProviderArtifact(child, "auth.json")
			unix.Close(child)
			if err != nil {
				return err
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}

func removeProviderArtifact(fd int, name string) error {
	var info unix.Stat_t
	if err := unix.Fstatat(fd, name, &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if err == unix.ENOENT {
			return nil
		}
		return err
	}
	if info.Mode&unix.S_IFMT != unix.S_IFREG || int(info.Uid) != os.Getuid() || uint64(info.Nlink) != 1 {
		return fmt.Errorf("The previous provider artifact is not an owned regular file.")
	}
	if err := unix.Unlinkat(fd, name, 0); err != nil {
		return err
	}
	return unix.Fsync(fd)
}
