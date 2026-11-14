package verbs

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// snapshotRunArtifact preserves submitted report bytes beside the source file.
// The shared directory preserves relative links in the report.
func snapshotRunArtifact(id *RunIdentity, path string, maximum int64) (string, error) {
	if path == "" {
		return "", nil
	}
	clean := filepath.Clean(path)
	var plane string
	for _, candidate := range id.ArtifactPlanes() {
		if strings.HasPrefix(clean, candidate+string(filepath.Separator)) {
			plane = candidate
			break
		}
	}
	if plane == "" {
		return "", Domainf("the report snapshot requires an authorized artifact plane")
	}
	root, err := os.OpenRoot(plane)
	if err != nil {
		return "", err
	}
	defer root.Close()
	relative, err := filepath.Rel(plane, clean)
	if err != nil {
		return "", err
	}
	info, err := root.Lstat(relative)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", Domainf("the report snapshot requires a regular source file")
	}
	source, err := root.Open(relative)
	if err != nil {
		return "", err
	}
	opened, err := source.Stat()
	if err != nil {
		source.Close()
		return "", err
	}
	if !os.SameFile(info, opened) {
		source.Close()
		return "", Domainf("the report source changed before its snapshot")
	}
	body, err := io.ReadAll(io.LimitReader(source, maximum+1))
	after, statErr := source.Stat()
	source.Close()
	if err != nil {
		return "", err
	}
	if statErr != nil {
		return "", statErr
	}
	if len(body) == 0 || int64(len(body)) > maximum {
		return "", Domainf("the report snapshot exceeds its size limit or contains no bytes")
	}
	if after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return "", Domainf("the report source changed during its snapshot")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	name := filepath.Join(filepath.Dir(relative), "snapshot-"+digest+filepath.Ext(relative))
	token, err := newRunID()
	if err != nil {
		return "", err
	}
	temporary := filepath.Join(filepath.Dir(relative), ".snapshot-"+token)
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer root.Remove(temporary)
	if _, err := file.Write(body); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Chmod(0444); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	// The final name becomes visible only after the complete bytes reach storage.
	if err := root.Link(temporary, name); os.IsExist(err) {
		info, err := root.Lstat(name)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", Domainf("the report snapshot destination requires a regular file")
		}
		existing, err := root.Open(name)
		if err != nil {
			return "", err
		}
		opened, err := existing.Stat()
		if err != nil {
			existing.Close()
			return "", err
		}
		if !os.SameFile(info, opened) {
			existing.Close()
			return "", Domainf("the report snapshot destination changed")
		}
		previous, err := io.ReadAll(io.LimitReader(existing, maximum+1))
		existing.Close()
		if err != nil {
			return "", err
		}
		if !bytes.Equal(previous, body) {
			return "", Domainf("the report snapshot bytes differ from their recorded identity")
		}
	} else if err != nil {
		return "", err
	}
	if err := root.Remove(temporary); err != nil {
		return "", err
	}
	directory, err := root.Open(filepath.Dir(name))
	if err != nil {
		return "", err
	}
	err = directory.Sync()
	directory.Close()
	if err != nil {
		return "", err
	}

	return filepath.Join(plane, name), nil
}
