package mclog

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	ROTATE_MAX_BYTES = 16 << 20
	ROTATE_KEEP      = 4

	rotateLockStale = 60 * time.Second
)

func (s *logFile) maybeRotate() {
	info, err := s.f.Stat()
	if err != nil || info.Size() < s.maxBytes {
		return
	}
	if !acquireRotateLock(s.path) {
		return
	}
	defer os.Remove(s.path + ".rotate.lock")

	current, err := os.Stat(s.path)
	if err != nil || current.Size() < s.maxBytes {
		s.reopen()
		return
	}
	if err := os.Rename(s.path, s.path+".rotating"); err != nil {
		return
	}
	s.reopen()

	shiftRing(s.path)
	if err := compressRotated(s.path+".rotating", s.path+".1.gz"); err != nil {
		return
	}
	if err := os.Remove(s.path + ".rotating"); err != nil {
		return
	}
	_ = syncDir(filepath.Dir(s.path))
}

func acquireRotateLock(path string) bool {
	lock := path + ".rotate.lock"
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err == nil {
		f.Close()
		return true
	}
	if !os.IsExist(err) {
		return false
	}
	info, statErr := os.Stat(lock)
	if statErr != nil || time.Since(info.ModTime()) < rotateLockStale {
		return false
	}
	if os.Remove(lock) != nil {
		return false
	}
	f, err = os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

func shiftRing(path string) {
	_ = os.Remove(fmt.Sprintf("%s.%d.gz", path, ROTATE_KEEP))
	for i := ROTATE_KEEP - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d.gz", path, i), fmt.Sprintf("%s.%d.gz", path, i+1))
	}
}

func compressRotated(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	next := dst + ".next"
	out, err := os.OpenFile(next, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		if removeErr := os.Remove(next); removeErr != nil {
			return removeErr
		}
		out, err = os.OpenFile(next, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	}
	if err != nil {
		return err
	}

	zw := gzip.NewWriter(out)
	_, err = io.Copy(zw, in)
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = out.Sync()
	}
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(next)
		return err
	}
	if err := os.Rename(next, dst); err != nil {
		_ = os.Remove(next)
		return err
	}
	return nil
}

func (s *logFile) reopen() {
	if s.closed {
		return
	}
	f, err := os.OpenFile(s.path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	_ = s.f.Close()
	s.f = f
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
