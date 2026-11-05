package distribution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureRecord() Record {
	r := Record{Format: 1, Commit: strings.Repeat("a", 40), ReleaseID: strings.Repeat("a", 40), Tag: "culler-" + strings.Repeat("a", 40), Sequence: 7, PublishedAt: time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC), Platform: "darwin-arm64", Schema: strings.Repeat("f", 64), Runtimes: RuntimeVersions{Bun: "1.3.9", Go: "1.25.12"}, Image: "ghcr.io/owner/culler@sha256:" + strings.Repeat("b", 64), Installer: Asset{Name: "installer.sh", SHA256: strings.Repeat("c", 64), Size: 12}, Package: Asset{Name: "culler-darwin-arm64.tar.gz", SHA256: strings.Repeat("d", 64), Size: 14}, AgentsView: &AgentsViewRelease{Version: "0.44.0", ArchiveSHA256: strings.Repeat("1", 64), ExecutableSHA256: strings.Repeat("2", 64)}, HostBun: fixtureHostBun()}
	for _, name := range RequiredChecks {
		r.Checks = append(r.Checks, Check{Name: name, Passed: true})
	}
	return r
}
func fixtureHostBun() *HostBunRelease {
	return &HostBunRelease{Version: "1.3.9", Archives: map[string]HostBunArchive{
		HostBunPlatform: {Name: "bun-darwin-aarch64.zip", URL: "https://github.com/oven-sh/bun/releases/download/bun-v1.3.9/bun-darwin-aarch64.zip", Size: 22000000, SHA256: strings.Repeat("3", 64)},
	}}
}
func fixtureClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := NewClient("owner/culler", "private-fixture-token")
	client.BaseURL = server.URL
	client.HTTP = server.Client()
	return client
}
func TestLatestCompleteRelease(t *testing.T) {
	record := fixtureRecord()
	client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cache-Control") != "no-cache" {
			t.Error("fresh release request omitted cache control")
		}
		if r.Header.Get("Authorization") != "Bearer private-fixture-token" {
			t.Error("credential absent")
		}
		switch r.URL.Path {
		case "/repos/owner/culler/releases/latest":
			_ = json.NewEncoder(w).Encode(githubRelease{Tag: record.Tag, Assets: []releaseAsset{{ID: 1, Name: RecordName}, {ID: 2, Name: record.Installer.Name, Size: record.Installer.Size}, {ID: 3, Name: record.Package.Name, Size: record.Package.Size}}})
		case "/repos/owner/culler/releases/assets/1":
			_ = json.NewEncoder(w).Encode(record)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	got, err := client.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != record.Commit {
		t.Fatal("wrong release")
	}
}
func TestIncompleteReleaseRejected(t *testing.T) {
	for _, change := range []struct {
		name   string
		mutate func(*Record)
	}{
		{"failed-check", func(r *Record) { r.Checks[0].Passed = false }},
		{"missing-check", func(r *Record) { r.Checks = r.Checks[1:] }},
		{"duplicate-check", func(r *Record) { r.Checks = append(r.Checks, r.Checks[0]) }},
		{"mutable-image", func(r *Record) { r.Image = "ghcr.io/owner/culler:latest" }},
		{"commit-mismatch", func(r *Record) { r.ReleaseID = strings.Repeat("b", 40) }},
		{"checksum", func(r *Record) { r.Package.SHA256 = "invalid" }},
		{"agentsview-version", func(r *Record) { r.AgentsView.Version = "latest" }},
		{"agentsview-archive", func(r *Record) { r.AgentsView.ArchiveSHA256 = strings.Repeat("A", 64) }},
		{"agentsview-executable", func(r *Record) { r.AgentsView.ExecutableSHA256 = "" }},
		{"hostbun-version", func(r *Record) { r.HostBun.Version = "latest" }},
		{"hostbun-no-darwin-arm64", func(r *Record) {
			r.HostBun.Archives = map[string]HostBunArchive{"linux-arm64": r.HostBun.Archives[HostBunPlatform]}
		}},
		{"hostbun-archive-digest", func(r *Record) {
			archive := r.HostBun.Archives[HostBunPlatform]
			archive.SHA256 = strings.Repeat("A", 64)
			r.HostBun.Archives[HostBunPlatform] = archive
		}},
		{"hostbun-archive-url", func(r *Record) {
			archive := r.HostBun.Archives[HostBunPlatform]
			archive.URL = "http://example.com/bun.zip"
			r.HostBun.Archives[HostBunPlatform] = archive
		}},
		{"hostbun-archive-size", func(r *Record) {
			archive := r.HostBun.Archives[HostBunPlatform]
			archive.Size = 0
			r.HostBun.Archives[HostBunPlatform] = archive
		}},
		{"hostbun-archive-name", func(r *Record) {
			archive := r.HostBun.Archives[HostBunPlatform]
			archive.Name = "../bun.zip"
			r.HostBun.Archives[HostBunPlatform] = archive
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			r := fixtureRecord()
			change.mutate(&r)
			if r.Validate() == nil {
				t.Fatal("incomplete release accepted")
			}
		})
	}
}
func TestPrivateAccessDistinguishedFromAbsentRelease(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-release", true: "no-access"}[private], func(t *testing.T) {
			client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/repos/owner/culler" && !private {
					w.Write([]byte(`{}`))
					return
				}
				http.NotFound(w, r)
			})
			_, err := client.Latest(context.Background())
			want := ErrNoRelease
			if private {
				want = ErrAccess
			}
			if !errors.Is(err, want) {
				t.Fatalf("got %v, want %v", err, want)
			}
		})
	}
}
func TestDownloadPinsTagAndRejectsWrongBytes(t *testing.T) {
	record := fixtureRecord()
	payload := []byte("verified installer")
	digest := sha256.Sum256(payload)
	record.Installer.Size = int64(len(payload))
	record.Installer.SHA256 = hex.EncodeToString(digest[:])
	wrong := false
	client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/culler/releases/tags/" + record.Tag:
			_ = json.NewEncoder(w).Encode(githubRelease{Tag: record.Tag, Assets: []releaseAsset{{ID: 2, Name: record.Installer.Name, Size: record.Installer.Size}}})
		case "/repos/owner/culler/releases/assets/2":
			if wrong {
				w.Write([]byte("malicious bytes!!!"))
			} else {
				w.Write(payload)
			}
		default:
			t.Errorf("download lost selected release: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	dir := t.TempDir()
	target := filepath.Join(dir, "installer.sh")
	if err := client.Download(context.Background(), record, record.Installer, target); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != string(payload) {
		t.Fatal("download bytes changed")
	}
	wrong = true
	target = filepath.Join(dir, "rejected.sh")
	if err := client.Download(context.Background(), record, record.Installer, target); err == nil {
		t.Fatal("bad bytes accepted")
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("invalid download created output")
	}
}
func TestAssetRedirectDoesNotForwardCredential(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("redirect leaked token")
		}
		w.Write([]byte("ok"))
	}))
	defer target.Close()
	client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) })
	data, err := client.get(context.Background(), "/asset", "application/octet-stream", 10)
	if err != nil || string(data) != "ok" {
		t.Fatalf("%s %v", data, err)
	}
}
func TestWorkflowUsesCurrentMainCommit(t *testing.T) {
	head := strings.Repeat("a", 40)
	for _, stale := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "stale"}[stale], func(t *testing.T) {
			client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/commits/main") {
					json.NewEncoder(w).Encode(map[string]string{"sha": head})
					return
				}
				if r.URL.Query().Get("head_sha") != head {
					t.Error("workflow query omitted current commit")
				}
				commit := head
				if stale {
					commit = strings.Repeat("b", 40)
				}
				json.NewEncoder(w).Encode(map[string]any{"workflow_runs": []any{map[string]string{"head_sha": commit, "status": "in_progress", "html_url": "https://github.com/owner/culler/actions/runs/1"}}})
			})
			got, err := client.Workflow(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			want := "preparing"
			if stale {
				want = "unknown"
			}
			if got.State != want || got.Commit != head {
				t.Fatalf("wrong workflow: %+v", got)
			}
		})
	}
}
func TestCredentialFileRequiresPrivateOwnerMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	t.Setenv("CULLER_GITHUB_TOKEN_FILE", path)
	if err := os.WriteFile(path, []byte("fixture-token\n"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Credential(context.Background())
	if err != nil || got != "fixture-token" {
		t.Fatalf("%q %v", got, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Credential(context.Background()); err == nil {
		t.Fatal("public credential accepted")
	}
}
func TestSaveLoadPrivateRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), InstalledRecord)
	record := fixtureRecord()
	if err := Save(path, record); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || got.Commit != record.Commit {
		t.Fatalf("%+v %v", got, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("record is not private")
	}
}

func TestCredentialFindsGitHubOutsideServicePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("CULLER_GITHUB_TOKEN_FILE", "")
	directory := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "gh"), []byte("#!/bin/sh\nprintf 'service_fixture_token\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	token, err := Credential(context.Background())
	if err != nil || token != "service_fixture_token" {
		t.Fatalf("credential = %q, %v", token, err)
	}
}
func TestARecordBeforeTheAgentsViewPackageStillLoads(t *testing.T) {
	r := fixtureRecord()
	r.AgentsView = nil
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "agentsview") {
		t.Fatalf("an older record gained an AgentsView key: %s", body)
	}
	decoded, err := Decode(body)
	if err != nil || decoded.AgentsView != nil {
		t.Fatalf("an older record = %+v, %v", decoded.AgentsView, err)
	}
	r = fixtureRecord()
	path := filepath.Join(t.TempDir(), "release.json")
	if err := Save(path, r); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.AgentsView == nil || *loaded.AgentsView != *r.AgentsView {
		t.Fatalf("the AgentsView identity did not survive a save: %+v, %v", loaded.AgentsView, err)
	}
}
func TestARecordBeforeTheHostBunInstallStillLoads(t *testing.T) {
	r := fixtureRecord()
	r.HostBun = nil
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "host_bun") {
		t.Fatalf("an older record gained a host Bun key: %s", body)
	}
	decoded, err := Decode(body)
	if err != nil || decoded.HostBun != nil {
		t.Fatalf("an older record = %+v, %v", decoded.HostBun, err)
	}
	r = fixtureRecord()
	path := filepath.Join(t.TempDir(), "release.json")
	if err := Save(path, r); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil || loaded.HostBun == nil || loaded.HostBun.Version != r.HostBun.Version ||
		loaded.HostBun.Archives[HostBunPlatform] != r.HostBun.Archives[HostBunPlatform] {
		t.Fatalf("the host Bun identity did not survive a save: %+v, %v", loaded.HostBun, err)
	}
}
