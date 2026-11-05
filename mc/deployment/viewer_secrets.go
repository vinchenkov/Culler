package deployment

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// A viewer secret is 64 lowercase hexadecimal characters from 32 random
// bytes. The AgentsView token and the wiki key are viewer secrets. Only the
// operator account can read their files.
var viewerSecretPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

const (
	agentsViewTokenLabel = "AgentsView token"
	wikiKeyLabel         = "wiki key"
)

// ErrUnusableViewerSecret marks a viewer secret file that is present but that
// Culler cannot use. Setup keeps such a file, so the repair removes it first.
var ErrUnusableViewerSecret = errors.New("unusable viewer secret file")

// markedError keeps the operator text of an error and gives errors.Is a mark.
type markedError struct {
	text string
	mark error
}

func (e *markedError) Error() string        { return e.text }
func (e *markedError) Is(target error) bool { return target == e.mark }

func markedErrorf(mark error, format string, args ...any) error {
	return &markedError{text: fmt.Sprintf(format, args...), mark: mark}
}

// WikiKeyPath returns the file of the secret path prefix of the wiki address.
func WikiKeyPath(home string) string {
	return filepath.Join(home, "state", "wiki-key")
}

// EnsureAgentsViewToken returns the AgentsView token of the deployment at
// home. It creates the token file when the file is absent. It never rewrites
// the file. The file is outside the AgentsView data folder, so a loss of the
// AgentsView archive keeps the token.
func EnsureAgentsViewToken(home string) (string, error) {
	paths := AgentsViewPathsFor(home)
	if err := ensureOwnerDirectory(paths.Root, "AgentsView folder"); err != nil {
		return "", err
	}
	return ensureViewerSecret(paths.Token, agentsViewTokenLabel)
}

// ReadAgentsViewToken returns the AgentsView token. The error text is
// operator text.
func ReadAgentsViewToken(home string) (string, error) {
	return readViewerSecret(AgentsViewPathsFor(home).Token, agentsViewTokenLabel)
}

// EnsureWikiKey returns the secret path prefix of the wiki address. It
// creates the key file when the file is absent. It never rewrites the file.
func EnsureWikiKey(home string) (string, error) {
	if err := ensureOwnerDirectory(filepath.Join(home, "state"), "state folder"); err != nil {
		return "", err
	}
	return ensureViewerSecret(WikiKeyPath(home), wikiKeyLabel)
}

// ReadWikiKey returns the secret path prefix of the wiki address.
func ReadWikiKey(home string) (string, error) {
	return readViewerSecret(WikiKeyPath(home), wikiKeyLabel)
}

func ensureViewerSecret(path, label string) (string, error) {
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		return readViewerSecret(path, label)
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("generate the %s: %w", label, err)
	}
	secret := hex.EncodeToString(random)
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return "", fmt.Errorf("write the %s: %w", label, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	_, err = tmp.WriteString(secret + "\n")
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("write the %s: %w", label, err)
	}
	// A link keeps the first secret when two processes create the file at
	// the same time.
	if err := os.Link(tmpPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return readViewerSecret(path, label)
		}
		return "", fmt.Errorf("install the %s: %w", label, err)
	}
	if err := os.Remove(tmpPath); err != nil {
		return "", fmt.Errorf("install the %s: %w", label, err)
	}
	if err := syncDirectory(dir); err != nil {
		return "", err
	}
	return secret, nil
}

func readViewerSecret(path, label string) (string, error) {
	before, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("The %s file %s is missing.", label, path)
	}
	if err != nil {
		return "", markedErrorf(ErrUnusableViewerSecret, "Culler cannot inspect the %s file %s.", label, path)
	}
	uid, links, ok := statOwner(before)
	if !before.Mode().IsRegular() || before.Mode().Perm() != 0o600 || !ok || int(uid) != os.Getuid() || links != 1 {
		return "", markedErrorf(ErrUnusableViewerSecret, "The %s file %s must be a regular file with mode 0600 that the operator owns.", label, path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", markedErrorf(ErrUnusableViewerSecret, "Culler cannot open the %s file %s.", label, path)
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return "", markedErrorf(ErrUnusableViewerSecret, "The %s file %s changed while Culler read it.", label, path)
	}
	body, err := io.ReadAll(io.LimitReader(f, 128))
	secret := strings.TrimSuffix(string(body), "\n")
	if err != nil || !viewerSecretPattern.MatchString(secret) {
		return "", markedErrorf(ErrUnusableViewerSecret, "The %s file %s must hold 64 lowercase hexadecimal characters.", label, path)
	}
	return secret, nil
}
