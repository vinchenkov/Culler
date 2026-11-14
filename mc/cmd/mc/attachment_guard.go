package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
	"mc/verbs"
)

type attachmentHealthGuardFrame struct {
	Generation string `json:"generation"`
	Version    int    `json:"version"`
}

type attachmentHealthGuardReceipt struct {
	Generation string `json:"generation"`
	State      string `json:"state"`
	Version    int    `json:"version"`
}

func attachmentHealthGuardPath(home string) string {
	return filepath.Join(home, "health", "attachment-canary", "resident.lock")
}

func attachmentHealthEpochPath(home string) string {
	return filepath.Join(home, "health", "attachment-canary", "resident-epoch.lock")
}

type attachmentHealthLease struct {
	epoch *os.File
}

func (l *attachmentHealthLease) Close() error {
	if l == nil || l.epoch == nil {
		return nil
	}
	err := unix.Flock(int(l.epoch.Fd()), unix.LOCK_UN)
	closeErr := l.epoch.Close()
	l.epoch = nil
	if err != nil {
		return err
	}
	return closeErr
}

func runAttachmentHealthGuard(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	if len(args) != 1 {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("usage: mc __attachment-health-guard"))
	}
	id, err := verbs.LoadIdentity()
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	if err := verbs.RequireHostScope(id, "mc __attachment-health-guard"); err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	reader := bufio.NewReader(io.LimitReader(stdin, 1025))
	line, err := reader.ReadBytes('\n')
	if err != nil || len(line) < 2 || len(line) > 1024 {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("attachment health guard requires one bounded frame line"))
	}
	body := bytes.TrimSuffix(line, []byte{'\n'})
	var frame attachmentHealthGuardFrame
	if err := decodeAttachmentCanaryJSON(body, &frame); err != nil {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("attachment health guard frame is invalid"))
	}
	canonical, err := json.Marshal(frame)
	if err != nil || !bytes.Equal(body, canonical) ||
		frame.Version != 1 || !privateAttachmentGeneration(frame.Generation) {
		return writeVerbError(stdout, stderr,
			verbs.Usagef("attachment health guard frame is invalid"))
	}
	home, err := requireCurrentAttachmentCanaryReceipt(frame.Generation)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	lock, err := openAttachmentHealthGuard(home)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return writeVerbError(stdout, stderr,
			verbs.Domainf("another attachment health guard is already live"))
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	epoch, err := openAttachmentHealthEpoch(home)
	if err != nil {
		return writeVerbError(stdout, stderr, err)
	}
	defer epoch.Close()
	if err := unix.Flock(int(epoch.Fd()), unix.LOCK_SH); err != nil {
		return writeVerbError(stdout, stderr,
			fmt.Errorf("hold attachment health epoch: %w", err))
	}
	defer unix.Flock(int(epoch.Fd()), unix.LOCK_UN)
	if err := lock.Truncate(0); err != nil {
		return writeVerbError(stdout, stderr, fmt.Errorf("truncate attachment health guard: %w", err))
	}
	if _, err := lock.Seek(0, io.SeekStart); err != nil {
		return writeVerbError(stdout, stderr, fmt.Errorf("rewind attachment health guard: %w", err))
	}
	if _, err := lock.Write(canonical); err != nil {
		return writeVerbError(stdout, stderr, fmt.Errorf("write attachment health guard: %w", err))
	}
	if err := lock.Sync(); err != nil {
		return writeVerbError(stdout, stderr, fmt.Errorf("sync attachment health guard: %w", err))
	}
	if err := writeJSON(stdout, attachmentHealthGuardReceipt{
		Generation: frame.Generation, State: "guarded", Version: 1,
	}); err != nil {
		fmt.Fprintln(stderr, "mc: write attachment health guard receipt:", err)
		return 2
	}

	// The resident owns the write end. EOF is its liveness boundary: normal
	// shutdown closes it explicitly and a crash closes it in the kernel.
	_, _ = io.Copy(io.Discard, reader)

	// Refuse new broker leases before waiting for every already-authorized
	// helper transaction to release its shared epoch lease.
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		return writeVerbError(stdout, stderr,
			fmt.Errorf("release attachment health liveness: %w", err))
	}
	if err := unix.Flock(int(epoch.Fd()), unix.LOCK_UN); err != nil {
		return writeVerbError(stdout, stderr,
			fmt.Errorf("release attachment health epoch: %w", err))
	}
	if err := unix.Flock(int(epoch.Fd()), unix.LOCK_EX); err != nil {
		return writeVerbError(stdout, stderr,
			fmt.Errorf("drain attachment health epoch: %w", err))
	}
	return 0
}

func openAttachmentHealthGuard(home string) (*os.File, error) {
	return openAttachmentHealthLock(attachmentHealthGuardPath(home))
}

func openAttachmentHealthEpoch(home string) (*os.File, error) {
	return openAttachmentHealthLock(attachmentHealthEpochPath(home))
}

func openAttachmentHealthLock(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open attachment health guard: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("adopt attachment health guard descriptor")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil ||
		stat.Mode&unix.S_IFMT != unix.S_IFREG ||
		stat.Mode&0o777 != 0o600 || int(stat.Uid) != os.Getuid() {
		file.Close()
		return nil, verbs.Domainf("attachment health guard has unsafe identity or mode")
	}
	return file, nil
}

func acquireLiveAttachmentHealthGuard(home, generation string) (*attachmentHealthLease, error) {
	epoch, err := openAttachmentHealthEpoch(home)
	if err != nil {
		return nil, err
	}
	lease := &attachmentHealthLease{epoch: epoch}
	if err := unix.Flock(int(epoch.Fd()), unix.LOCK_SH); err != nil {
		_ = lease.Close()
		return nil, fmt.Errorf("acquire attachment health epoch: %w", err)
	}
	file, err := openAttachmentHealthGuard(home)
	if err != nil {
		_ = lease.Close()
		return nil, err
	}
	defer file.Close()
	err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = lease.Close()
		return nil, verbs.Domainf("attachment health guard is not live for this resident boot")
	}
	if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
		_ = lease.Close()
		return nil, fmt.Errorf("probe attachment health guard lock: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = lease.Close()
		return nil, fmt.Errorf("rewind attachment health guard: %w", err)
	}
	body, err := io.ReadAll(io.LimitReader(file, 1025))
	if err != nil {
		_ = lease.Close()
		return nil, fmt.Errorf("read attachment health guard: %w", err)
	}
	var frame attachmentHealthGuardFrame
	if len(body) == 0 || len(body) > 1024 ||
		decodeAttachmentCanaryJSON(body, &frame) != nil ||
		frame.Version != 1 || frame.Generation != generation {
		_ = lease.Close()
		return nil, verbs.Domainf("attachment health guard does not match this resident boot")
	}
	canonical, err := json.Marshal(frame)
	if err != nil || !bytes.Equal(body, canonical) {
		_ = lease.Close()
		return nil, verbs.Domainf("attachment health guard is malformed")
	}
	return lease, nil
}
