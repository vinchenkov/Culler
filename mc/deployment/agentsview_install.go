package deployment

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// AgentsViewMismatchError names the AgentsView file that differs from the
// pin, with the expected and the actual size and digest.
type AgentsViewMismatchError struct {
	Item           string
	ExpectedSize   int64
	ExpectedSHA256 string
	ActualSize     int64
	ActualSHA256   string
	// Larger is true when the file holds more bytes than the pin. The copy
	// then stops, so the actual size and digest are unknown.
	Larger bool
}

func (e *AgentsViewMismatchError) Error() string {
	if e.Larger {
		return fmt.Sprintf("the AgentsView %s differs from its pin: expected %d bytes with SHA-256 %s, found more than %d bytes",
			e.Item, e.ExpectedSize, e.ExpectedSHA256, e.ExpectedSize)
	}
	return fmt.Sprintf("the AgentsView %s differs from its pin: expected %d bytes with SHA-256 %s, found %d bytes with SHA-256 %s",
		e.Item, e.ExpectedSize, e.ExpectedSHA256, e.ActualSize, e.ActualSHA256)
}

// AgentsViewDownload selects the source of the pinned archive. The zero value
// uses the pinned URL and a default client. Tests supply a local server.
type AgentsViewDownload struct {
	Client *http.Client
	URL    string
}

const agentsViewDownloadTimeout = 15 * time.Minute

func copyAgentsViewVerified(w io.Writer, r io.Reader, item string, size int64, digest string) error {
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(w, hash), io.LimitReader(r, size+1))
	if err != nil {
		return fmt.Errorf("read the AgentsView %s: %w", item, err)
	}
	mismatch := &AgentsViewMismatchError{Item: item, ExpectedSize: size, ExpectedSHA256: digest, ActualSize: n}
	if n > size {
		mismatch.Larger = true
		return mismatch
	}
	mismatch.ActualSHA256 = hex.EncodeToString(hash.Sum(nil))
	if n != size || mismatch.ActualSHA256 != digest {
		return mismatch
	}
	return nil
}

func verifyAgentsViewFile(path, item string, size int64, digest string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect the AgentsView %s: %w", item, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("the AgentsView %s %q must be a regular file", item, path)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open the AgentsView %s: %w", item, err)
	}
	defer file.Close()
	hash := sha256.New()
	n, err := io.Copy(hash, file)
	if err != nil {
		return fmt.Errorf("read the AgentsView %s: %w", item, err)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if n != size || actual != digest {
		return &AgentsViewMismatchError{Item: item, ExpectedSize: size, ExpectedSHA256: digest, ActualSize: n, ActualSHA256: actual}
	}
	return nil
}

// VerifyAgentsViewArchive checks the size and SHA-256 of a release archive.
func VerifyAgentsViewArchive(path string, pin AgentsViewPin) error {
	return verifyAgentsViewFile(path, "release archive", pin.Archive.Size, pin.Archive.SHA256)
}

// VerifyAgentsViewExecutable checks the size and SHA-256 of an executable.
func VerifyAgentsViewExecutable(path string, pin AgentsViewPin) error {
	return verifyAgentsViewFile(path, "executable", pin.Executable.Size, pin.Executable.SHA256)
}

// stageAgentsViewExecutable writes the verified bytes of r to a new 0755
// file in directory and returns its path.
func stageAgentsViewExecutable(directory string, r io.Reader, pin AgentsViewPin) (string, error) {
	file, err := os.CreateTemp(directory, ".agentsview-")
	if err != nil {
		return "", fmt.Errorf("create the AgentsView staging file: %w", err)
	}
	path := file.Name()
	err = copyAgentsViewVerified(file, r, "executable", pin.Executable.Size, pin.Executable.SHA256)
	if err == nil {
		err = file.Chmod(0o755)
	}
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// ExtractAgentsViewExecutable verifies the archive, then writes its single
// regular entry to a new 0755 file in directory after it verifies that entry.
func ExtractAgentsViewExecutable(archive string, pin AgentsViewPin, directory string) (string, error) {
	if err := VerifyAgentsViewArchive(archive, pin); err != nil {
		return "", err
	}
	file, err := os.Open(archive)
	if err != nil {
		return "", fmt.Errorf("open the AgentsView release archive: %w", err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return "", fmt.Errorf("read the AgentsView release archive: %w", err)
	}
	defer compressed.Close()
	entries := tar.NewReader(compressed)
	header, err := entries.Next()
	if errors.Is(err, io.EOF) {
		return "", errors.New("the AgentsView release archive holds no executable")
	}
	if err != nil {
		return "", fmt.Errorf("read the AgentsView release archive: %w", err)
	}
	if header.Name != pin.Executable.Name {
		return "", fmt.Errorf("the AgentsView release archive holds the entry %q; it must hold only %q", header.Name, pin.Executable.Name)
	}
	if header.Typeflag != tar.TypeReg {
		return "", fmt.Errorf("the AgentsView release archive entry %q must be a regular file", header.Name)
	}
	staged, err := stageAgentsViewExecutable(directory, entries, pin)
	if err != nil {
		return "", err
	}
	if _, err := entries.Next(); !errors.Is(err, io.EOF) {
		_ = os.Remove(staged)
		if err != nil {
			return "", fmt.Errorf("read the AgentsView release archive: %w", err)
		}
		return "", fmt.Errorf("the AgentsView release archive must hold only %q", pin.Executable.Name)
	}
	return staged, nil
}

func agentsViewHTTPSRedirect(request *http.Request, via []*http.Request) error {
	if request.URL.Scheme != "https" {
		return errors.New("the AgentsView download redirect requires HTTPS")
	}
	if len(via) >= 10 {
		return errors.New("the AgentsView download has too many redirects")
	}
	return nil
}

// DownloadAgentsViewArchive fetches the pinned archive into a new file in
// directory. It returns the path only after the size and SHA-256 match.
func DownloadAgentsViewArchive(ctx context.Context, pin AgentsViewPin, source AgentsViewDownload, directory string) (string, error) {
	address := source.URL
	if address == "" {
		address = pin.Archive.URL
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("the AgentsView download address must be an HTTPS URL")
	}
	client := http.Client{Timeout: agentsViewDownloadTimeout}
	if source.Client != nil {
		client = *source.Client
	}
	client.CheckRedirect = agentsViewHTTPSRedirect
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return "", fmt.Errorf("prepare the AgentsView download: %w", err)
	}
	request.Header.Set("Accept", "application/octet-stream")
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("download the AgentsView release archive: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download the AgentsView release archive: the server returned HTTP %d", response.StatusCode)
	}
	file, err := os.CreateTemp(directory, ".agentsview-archive-")
	if err != nil {
		return "", fmt.Errorf("create the AgentsView download file: %w", err)
	}
	path := file.Name()
	err = copyAgentsViewVerified(file, response.Body, "release archive", pin.Archive.Size, pin.Archive.SHA256)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// agentsViewBin returns MC_HOME/bin after it checks MC_HOME and creates the
// folder when it is absent.
func agentsViewBin(home string) (string, AgentsViewPaths, error) {
	canonical, err := CanonicalHome(home)
	if err != nil {
		return "", AgentsViewPaths{}, fmt.Errorf("resolve the AgentsView MC_HOME: %w", err)
	}
	if err := requireOwnerOnlyDirectory(canonical, "MC_HOME"); err != nil {
		return "", AgentsViewPaths{}, err
	}
	paths := AgentsViewPathsFor(canonical)
	bin := filepath.Dir(paths.Executable)
	if err := os.Mkdir(bin, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", AgentsViewPaths{}, fmt.Errorf("create %q: %w", bin, err)
	}
	if err := requireOperatorSourceDirectory(bin, "executable directory"); err != nil {
		return "", AgentsViewPaths{}, err
	}
	return bin, paths, nil
}

// installedAgentsView reports "ok" when the installed executable matches the
// pin, "done" when only its mode needed a repair, and "" when it must be
// replaced.
func installedAgentsView(path string, pin AgentsViewPin) (string, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect the installed AgentsView executable: %w", err)
	}
	uid, links, ok := statOwner(info)
	if !info.Mode().IsRegular() || !ok || int(uid) != os.Getuid() || links != 1 || info.Size() != pin.Executable.Size {
		return "", nil
	}
	var mismatch *AgentsViewMismatchError
	if err := VerifyAgentsViewExecutable(path, pin); errors.As(err, &mismatch) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	if info.Mode().Perm() == 0o755 {
		return "ok", nil
	}
	if err := os.Chmod(path, 0o755); err != nil {
		return "", fmt.Errorf("repair the AgentsView executable mode: %w", err)
	}
	return "done", nil
}

func commitAgentsView(staged, target string) error {
	if err := os.Rename(staged, target); err != nil {
		_ = os.Remove(staged)
		return fmt.Errorf("install the AgentsView executable: %w", err)
	}
	return syncDirectory(filepath.Dir(target))
}

// InstallAgentsViewExecutable verifies source against the pin and installs it
// at MC_HOME/bin/agentsview. It returns "ok" when the installed executable
// already matches the pin, and "done" after a change.
func InstallAgentsViewExecutable(home, source string, pin AgentsViewPin) (string, string, error) {
	if !filepath.IsAbs(source) {
		return "", "", errors.New("the AgentsView executable path must be absolute")
	}
	if err := VerifyAgentsViewExecutable(source, pin); err != nil {
		return "", "", err
	}
	bin, paths, err := agentsViewBin(home)
	if err != nil {
		return "", "", err
	}
	status, err := installedAgentsView(paths.Executable, pin)
	if err != nil || status != "" {
		return status, paths.Executable, err
	}
	input, err := os.Open(source)
	if err != nil {
		return "", "", fmt.Errorf("open the AgentsView executable: %w", err)
	}
	defer input.Close()
	staged, err := stageAgentsViewExecutable(bin, input, pin)
	if err != nil {
		return "", "", err
	}
	if err := commitAgentsView(staged, paths.Executable); err != nil {
		return "", "", err
	}
	return "done", paths.Executable, nil
}

// EnsureAgentsView installs the pinned executable at MC_HOME/bin/agentsview.
// It downloads the pinned archive only when the installed executable differs
// from the pin.
func EnsureAgentsView(ctx context.Context, home string, pin AgentsViewPin, source AgentsViewDownload) (string, string, error) {
	bin, paths, err := agentsViewBin(home)
	if err != nil {
		return "", "", err
	}
	status, err := installedAgentsView(paths.Executable, pin)
	if err != nil || status != "" {
		return status, paths.Executable, err
	}
	scratch, err := os.MkdirTemp(bin, ".agentsview-download-")
	if err != nil {
		return "", "", fmt.Errorf("create the AgentsView download folder: %w", err)
	}
	defer os.RemoveAll(scratch)
	archive, err := DownloadAgentsViewArchive(ctx, pin, source, scratch)
	if err != nil {
		return "", "", err
	}
	staged, err := ExtractAgentsViewExecutable(archive, pin, bin)
	if err != nil {
		return "", "", err
	}
	if err := commitAgentsView(staged, paths.Executable); err != nil {
		return "", "", err
	}
	return "done", paths.Executable, nil
}
