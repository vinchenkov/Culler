package deployment

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

var agentsViewFixtureExecutable = []byte("#!/bin/sh\necho synthetic agentsview\n")

type agentsViewTarEntry struct {
	name     string
	typeflag byte
	body     []byte
	link     string
}

func agentsViewDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func agentsViewArchiveBytes(t *testing.T, entries ...agentsViewTarEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Typeflag: entry.typeflag, Mode: 0o755, Linkname: entry.link}
		if entry.typeflag == tar.TypeReg {
			header.Size = int64(len(entry.body))
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.typeflag == tar.TypeReg {
			if _, err := archive.Write(entry.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// agentsViewInstallFixture returns an archive and a pin that names that
// archive and the synthetic executable.
func agentsViewInstallFixture(t *testing.T, entries ...agentsViewTarEntry) ([]byte, AgentsViewPin) {
	t.Helper()
	if len(entries) == 0 {
		entries = []agentsViewTarEntry{{name: "agentsview", typeflag: tar.TypeReg, body: agentsViewFixtureExecutable}}
	}
	archive := agentsViewArchiveBytes(t, entries...)
	pin := AgentsViewPin{
		Version:  "1.2.3",
		Platform: "darwin-arm64",
		Archive: AgentsViewArchive{
			Name:   "agentsview_1.2.3_darwin_arm64.tar.gz",
			URL:    "https://example.com/releases/download/v1.2.3/agentsview_1.2.3_darwin_arm64.tar.gz",
			Size:   int64(len(archive)),
			SHA256: agentsViewDigest(archive),
		},
		Executable: AgentsViewExecutable{
			Name:   "agentsview",
			Size:   int64(len(agentsViewFixtureExecutable)),
			SHA256: agentsViewDigest(agentsViewFixtureExecutable),
		},
		DisabledAgents: []string{"cursor"},
	}
	if err := pin.validate(); err != nil {
		t.Fatalf("the fixture pin is invalid: %v", err)
	}
	return archive, pin
}

func writeAgentsViewFixture(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func agentsViewHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := CanonicalHome(home)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func requireAgentsViewMismatch(t *testing.T, err error, item, expected string) {
	t.Helper()
	var mismatch *AgentsViewMismatchError
	if !errors.As(err, &mismatch) || mismatch.Item != item {
		t.Fatalf("error = %v, want an AgentsView %s mismatch", err, item)
	}
	text := err.Error()
	if !strings.Contains(text, "AgentsView "+item) || !strings.Contains(text, expected) {
		t.Fatalf("the mismatch text omits AgentsView or the expected digest: %s", text)
	}
	if !mismatch.Larger && !strings.Contains(text, mismatch.ActualSHA256) {
		t.Fatalf("the mismatch text omits the actual digest: %s", text)
	}
}

func requireNoAgentsViewStage(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".agentsview") {
			t.Fatalf("a staging entry remains: %s", entry.Name())
		}
	}
}

func TestAgentsViewArchiveVerificationNamesBothDigests(t *testing.T) {
	archive, pin := agentsViewInstallFixture(t)
	if err := VerifyAgentsViewArchive(writeAgentsViewFixture(t, "good.tar.gz", archive), pin); err != nil {
		t.Fatal(err)
	}
	changed := append(append([]byte(nil), archive...), 0)
	err := VerifyAgentsViewArchive(writeAgentsViewFixture(t, "changed.tar.gz", changed), pin)
	requireAgentsViewMismatch(t, err, "release archive", pin.Archive.SHA256)
	if !strings.Contains(err.Error(), agentsViewDigest(changed)) {
		t.Fatalf("the mismatch omits the actual digest: %v", err)
	}
}

func TestAgentsViewExtractionInstallsOnlyTheSingleRegularEntry(t *testing.T) {
	archive, pin := agentsViewInstallFixture(t)
	directory := t.TempDir()
	staged, err := ExtractAgentsViewExecutable(writeAgentsViewFixture(t, "archive.tar.gz", archive), pin, directory)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(staged)
	if err != nil || !bytes.Equal(body, agentsViewFixtureExecutable) {
		t.Fatalf("staged executable = %q, %v", body, err)
	}
	info, err := os.Stat(staged)
	if err != nil || info.Mode().Perm() != 0o755 || filepath.Dir(staged) != directory {
		t.Fatalf("staged executable %s: %v, %v", staged, info, err)
	}
}

func TestAgentsViewExtractionRefusesLinksOtherEntriesAndPathTricks(t *testing.T) {
	good := agentsViewTarEntry{name: "agentsview", typeflag: tar.TypeReg, body: agentsViewFixtureExecutable}
	for _, tc := range []struct {
		name    string
		entries []agentsViewTarEntry
		want    string
	}{
		{"symbolic link", []agentsViewTarEntry{{name: "agentsview", typeflag: tar.TypeSymlink, link: "/bin/sh"}}, "regular file"},
		{"hard link", []agentsViewTarEntry{{name: "agentsview", typeflag: tar.TypeLink, link: "other"}}, "regular file"},
		{"directory", []agentsViewTarEntry{{name: "agentsview/", typeflag: tar.TypeDir}}, "must hold only"},
		{"dot prefix", []agentsViewTarEntry{{name: "./agentsview", typeflag: tar.TypeReg, body: agentsViewFixtureExecutable}}, "must hold only"},
		{"parent path", []agentsViewTarEntry{{name: "../agentsview", typeflag: tar.TypeReg, body: agentsViewFixtureExecutable}}, "must hold only"},
		{"absolute path", []agentsViewTarEntry{{name: "/agentsview", typeflag: tar.TypeReg, body: agentsViewFixtureExecutable}}, "must hold only"},
		{"nested path", []agentsViewTarEntry{{name: "bin/agentsview", typeflag: tar.TypeReg, body: agentsViewFixtureExecutable}}, "must hold only"},
		{"second entry", []agentsViewTarEntry{good, {name: "extra", typeflag: tar.TypeReg, body: []byte("x")}}, "must hold only"},
		{"repeated entry", []agentsViewTarEntry{good, good}, "must hold only"},
		{"empty archive", nil, "holds no executable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var archive []byte
			var pin AgentsViewPin
			if tc.entries == nil {
				_, pin = agentsViewInstallFixture(t)
				archive = agentsViewArchiveBytes(t)
				pin.Archive.Size, pin.Archive.SHA256 = int64(len(archive)), agentsViewDigest(archive)
			} else {
				archive, pin = agentsViewInstallFixture(t, tc.entries...)
			}
			directory := t.TempDir()
			_, err := ExtractAgentsViewExecutable(writeAgentsViewFixture(t, "archive.tar.gz", archive), pin, directory)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "AgentsView") {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			requireNoAgentsViewStage(t, directory)
		})
	}
}

func TestAgentsViewExtractionVerifiesTheExecutableDigest(t *testing.T) {
	archive, pin := agentsViewInstallFixture(t, agentsViewTarEntry{name: "agentsview", typeflag: tar.TypeReg, body: []byte("#!/bin/sh\necho changed agentsview\n")})
	directory := t.TempDir()
	_, err := ExtractAgentsViewExecutable(writeAgentsViewFixture(t, "archive.tar.gz", archive), pin, directory)
	requireAgentsViewMismatch(t, err, "executable", pin.Executable.SHA256)
	requireNoAgentsViewStage(t, directory)
}

func TestAgentsViewExtractionVerifiesTheArchiveFirst(t *testing.T) {
	archive, pin := agentsViewInstallFixture(t)
	pin.Archive.SHA256 = strings.Repeat("0", 64)
	directory := t.TempDir()
	_, err := ExtractAgentsViewExecutable(writeAgentsViewFixture(t, "archive.tar.gz", archive), pin, directory)
	requireAgentsViewMismatch(t, err, "release archive", pin.Archive.SHA256)
	requireNoAgentsViewStage(t, directory)
}

func agentsViewServer(t *testing.T, body []byte, requests *atomic.Int32) *httptest.Server {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			requests.Add(1)
		}
		if r.Header.Get("Accept") != "application/octet-stream" {
			http.Error(w, "accept", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/archive":
			_, _ = w.Write(body)
		case "/insecure-redirect":
			http.Redirect(w, r, "http://127.0.0.1:9/archive", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func TestAgentsViewDownloadVerifiesTheArchive(t *testing.T) {
	archive, pin := agentsViewInstallFixture(t)
	server := agentsViewServer(t, archive, nil)
	source := AgentsViewDownload{Client: server.Client(), URL: server.URL + "/archive"}
	directory := t.TempDir()
	path, err := DownloadAgentsViewArchive(context.Background(), pin, source, directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAgentsViewArchive(path, pin); err != nil {
		t.Fatal(err)
	}

	changed := append([]byte(nil), archive...)
	changed[len(changed)/2] ^= 0xff
	server = agentsViewServer(t, changed, nil)
	directory = t.TempDir()
	_, err = DownloadAgentsViewArchive(context.Background(), pin, AgentsViewDownload{Client: server.Client(), URL: server.URL + "/archive"}, directory)
	requireAgentsViewMismatch(t, err, "release archive", pin.Archive.SHA256)
	requireNoAgentsViewStage(t, directory)

	server = agentsViewServer(t, append(append([]byte(nil), archive...), archive...), nil)
	_, err = DownloadAgentsViewArchive(context.Background(), pin, AgentsViewDownload{Client: server.Client(), URL: server.URL + "/archive"}, directory)
	var mismatch *AgentsViewMismatchError
	if !errors.As(err, &mismatch) || !mismatch.Larger || !strings.Contains(err.Error(), "found more than") {
		t.Fatalf("a larger download = %v", err)
	}
	requireNoAgentsViewStage(t, directory)
}

func TestAgentsViewDownloadRefusesPlainHTTPAndFailedResponses(t *testing.T) {
	archive, pin := agentsViewInstallFixture(t)
	server := agentsViewServer(t, archive, nil)
	directory := t.TempDir()
	for _, tc := range []struct {
		address string
		want    string
	}{
		{"http://127.0.0.1:9/archive", "HTTPS"},
		{server.URL + "/insecure-redirect", "requires HTTPS"},
		{server.URL + "/absent", "HTTP 404"},
	} {
		_, err := DownloadAgentsViewArchive(context.Background(), pin, AgentsViewDownload{Client: server.Client(), URL: tc.address}, directory)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %v, want %q", tc.address, err, tc.want)
		}
	}
	requireNoAgentsViewStage(t, directory)
}

func TestEnsureAgentsViewDownloadsOnlyWhenTheInstalledExecutableDiffers(t *testing.T) {
	archive, pin := agentsViewInstallFixture(t)
	var requests atomic.Int32
	server := agentsViewServer(t, archive, &requests)
	source := AgentsViewDownload{Client: server.Client(), URL: server.URL + "/archive"}
	home := agentsViewHome(t)
	target := filepath.Join(home, "bin", "agentsview")

	status, path, err := EnsureAgentsView(context.Background(), home, pin, source)
	if err != nil || status != "done" || path != target {
		t.Fatalf("first install = %q, %q, %v", status, path, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("installed executable: %v, %v", info, err)
	}
	if err := VerifyAgentsViewExecutable(target, pin); err != nil {
		t.Fatal(err)
	}
	requireNoAgentsViewStage(t, filepath.Join(home, "bin"))

	status, _, err = EnsureAgentsView(context.Background(), home, pin, source)
	if err != nil || status != "ok" || requests.Load() != 1 {
		t.Fatalf("current install = %q, %v after %d requests", status, err, requests.Load())
	}

	if err := os.Chmod(target, 0o700); err != nil {
		t.Fatal(err)
	}
	status, _, err = EnsureAgentsView(context.Background(), home, pin, source)
	info, statErr := os.Stat(target)
	if err != nil || status != "done" || requests.Load() != 1 || statErr != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("mode repair = %q, %v, %v after %d requests", status, err, info, requests.Load())
	}

	if err := os.WriteFile(target, []byte("tampered\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	status, _, err = EnsureAgentsView(context.Background(), home, pin, source)
	if err != nil || status != "done" || requests.Load() != 2 {
		t.Fatalf("replacement = %q, %v after %d requests", status, err, requests.Load())
	}
	if err := VerifyAgentsViewExecutable(target, pin); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureAgentsViewKeepsTheInstalledFileAfterAMismatch(t *testing.T) {
	archive, pin := agentsViewInstallFixture(t)
	changed := append([]byte(nil), archive...)
	changed[len(changed)-1] ^= 0xff
	server := agentsViewServer(t, changed, nil)
	home := agentsViewHome(t)
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "agentsview"), []byte("previous\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := EnsureAgentsView(context.Background(), home, pin, AgentsViewDownload{Client: server.Client(), URL: server.URL + "/archive"})
	requireAgentsViewMismatch(t, err, "release archive", pin.Archive.SHA256)
	body, readErr := os.ReadFile(filepath.Join(bin, "agentsview"))
	if readErr != nil || string(body) != "previous\n" {
		t.Fatalf("a failed install changed the installed file: %q, %v", body, readErr)
	}
	requireNoAgentsViewStage(t, bin)
}

func TestInstallAgentsViewExecutableVerifiesTheSourceFile(t *testing.T) {
	_, pin := agentsViewInstallFixture(t)
	home := agentsViewHome(t)
	target := filepath.Join(home, "bin", "agentsview")
	source := writeAgentsViewFixture(t, "agentsview", agentsViewFixtureExecutable)

	status, path, err := InstallAgentsViewExecutable(home, source, pin)
	if err != nil || status != "done" || path != target {
		t.Fatalf("install = %q, %q, %v", status, path, err)
	}
	if err := VerifyAgentsViewExecutable(target, pin); err != nil {
		t.Fatal(err)
	}
	status, _, err = InstallAgentsViewExecutable(home, source, pin)
	if err != nil || status != "ok" {
		t.Fatalf("repeat install = %q, %v", status, err)
	}

	wrong := writeAgentsViewFixture(t, "agentsview", []byte("#!/bin/sh\necho other\n"))
	_, _, err = InstallAgentsViewExecutable(home, wrong, pin)
	requireAgentsViewMismatch(t, err, "executable", pin.Executable.SHA256)
	if err := VerifyAgentsViewExecutable(target, pin); err != nil {
		t.Fatalf("a refused source changed the installed file: %v", err)
	}
	if _, _, err := InstallAgentsViewExecutable(home, "agentsview", pin); err == nil {
		t.Fatal("a relative source path was accepted")
	}
	requireNoAgentsViewStage(t, filepath.Join(home, "bin"))
}

func TestAgentsViewInstallRequiresAnOwnerOnlyHome(t *testing.T) {
	_, pin := agentsViewInstallFixture(t)
	home := agentsViewHome(t)
	if err := os.Chmod(home, 0o755); err != nil {
		t.Fatal(err)
	}
	source := writeAgentsViewFixture(t, "agentsview", agentsViewFixtureExecutable)
	if _, _, err := InstallAgentsViewExecutable(home, source, pin); err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("a shared MC_HOME was accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused install created MC_HOME/bin: %v", err)
	}
}

func TestTheHostReleaseShipsTheAgentsViewLicense(t *testing.T) {
	found := false
	for _, rel := range HostReleaseFiles() {
		if !strings.HasPrefix(rel, "third_party/") {
			continue
		}
		if filepath.Base(rel) != "LICENSE" {
			t.Fatalf("the host release ships the third-party file %s, which is not a license", rel)
		}
		found = found || rel == "third_party/agentsview/LICENSE"
	}
	if !found {
		t.Fatal("the host release omits the AgentsView license")
	}
}
