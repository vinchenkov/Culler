// Package distribution defines the published Culler release contract.
package distribution

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const DefaultRepository = "vinchenkov/Culler"
const RecordName = "release.json"
const InstalledRecord = "state/published-release.json"

var RequiredChecks = []string{"fast-suite-macos", "deployment-linux", "application-package", "product-image", "release-downloads"}
var hexDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)
var commitDigest = regexp.MustCompile(`^[0-9a-f]{40}$`)
var imageDigest = regexp.MustCompile(`^ghcr\.io/[a-z0-9][a-z0-9/._-]*@sha256:[0-9a-f]{64}$`)
var runtimeVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
var productVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type Asset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}
type RuntimeVersions struct {
	Bun string `json:"bun"`
	Go  string `json:"go"`
}

// AgentsViewRelease names the pinned AgentsView release that the package
// holds.
type AgentsViewRelease struct {
	Version          string `json:"version"`
	ArchiveSHA256    string `json:"archive_sha256"`
	ExecutableSHA256 string `json:"executable_sha256"`
}

// HostBunArchive names the Bun archive of one Mac architecture.
type HostBunArchive struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// HostBunRelease names the Bun release that the published installer puts
// under MC_HOME/bun/<version>/, with one archive for each supported Mac
// architecture.
type HostBunRelease struct {
	Version  string                    `json:"version"`
	Archives map[string]HostBunArchive `json:"archives"`
}

// HostBunPlatform is the archive key of a Mac with Apple silicon.
const HostBunPlatform = "darwin-arm64"

type Record struct {
	// Version is the VERSION file of the release commit. Tag is "v" + Version.
	Version         string          `json:"version"`
	OpenCode        *Asset          `json:"opencode,omitempty"`
	WorkflowSeconds int64           `json:"workflow_seconds,omitempty"`
	Format          int             `json:"format"`
	Commit          string          `json:"commit"`
	ReleaseID       string          `json:"release_id"`
	Tag             string          `json:"tag"`
	Sequence        int64           `json:"sequence"`
	PublishedAt     time.Time       `json:"published_at"`
	Platform        string          `json:"platform"`
	Schema          string          `json:"schema"`
	Runtimes        RuntimeVersions `json:"runtimes"`
	Installer       Asset           `json:"installer"`
	Package         Asset           `json:"package"`
	Image           string          `json:"image"`
	ProjectImage    string          `json:"project_image,omitempty"`
	Checks          []Check         `json:"checks"`
	// AgentsView is absent in a record of a release before the AgentsView
	// package. Setup requires it in the release that it installs.
	AgentsView *AgentsViewRelease `json:"agentsview,omitempty"`
	// HostBun is absent in a record of a release before the host Bun
	// install. Setup requires it in the release that it installs.
	HostBun *HostBunRelease `json:"host_bun,omitempty"`
}

// ValidVersion reports whether v is a MAJOR.MINOR.PATCH version without
// leading zeros.
func ValidVersion(v string) bool { return productVersion.MatchString(v) }

// CompareVersions returns -1, 0, or 1 when a is lower than, equal to, or
// higher than b. An absent or invalid version is lower than each valid
// version.
func CompareVersions(a, b string) int {
	parts := func(v string) []int {
		if !ValidVersion(v) {
			return nil
		}
		var n []int
		for _, field := range strings.Split(v, ".") {
			value, _ := strconv.Atoi(field)
			n = append(n, value)
		}
		return n
	}
	x, y := parts(a), parts(b)
	switch {
	case x == nil && y == nil:
		return 0
	case x == nil:
		return -1
	case y == nil:
		return 1
	}
	for i := range x {
		if x[i] != y[i] {
			if x[i] < y[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Label names a release for a person: the version and the short commit,
// for example "0.1.0 (1a2b3c4d5e6f)". An unknown version gives the short
// commit only.
func Label(version, commit string) string {
	short := commit
	if len(short) > 12 {
		short = short[:12]
	}
	if !ValidVersion(version) {
		return short
	}
	return version + " (" + short + ")"
}

// Label names the release for a person.
func (r Record) Label() string { return Label(r.Version, r.Commit) }

func (r Record) Validate() error {
	if r.Format != 1 {
		return errors.New("unsupported release record format")
	}
	if !ValidVersion(r.Version) {
		return errors.New("release version is invalid")
	}
	if !commitDigest.MatchString(r.Commit) || r.ReleaseID != r.Commit || r.Tag != "v"+r.Version {
		return errors.New("release identity does not match its commit")
	}
	if r.Sequence <= 0 || r.PublishedAt.IsZero() {
		return errors.New("release publication identity is absent")
	}
	if r.Platform != "darwin-arm64" {
		return errors.New("this release requires a Mac with Apple silicon")
	}
	if !hexDigest.MatchString(r.Schema) {
		return errors.New("release schema identity is invalid")
	}
	if !runtimeVersion.MatchString(r.Runtimes.Bun) || !runtimeVersion.MatchString(r.Runtimes.Go) {
		return errors.New("release runtime versions are invalid")
	}
	if !imageDigest.MatchString(r.Image) {
		return errors.New("release image must use a GitHub Container Registry digest")
	}
	if r.ProjectImage != "" && !imageDigest.MatchString(r.ProjectImage) {
		return errors.New("release Project image must use a GitHub Container Registry digest")
	}
	for _, a := range []struct {
		asset Asset
		name  string
	}{{r.Installer, "installer.sh"}, {r.Package, "culler-darwin-arm64.tar.gz"}} {
		if a.asset.Name != a.name || !hexDigest.MatchString(a.asset.SHA256) || a.asset.Size <= 0 {
			return fmt.Errorf("release asset %s is invalid", a.name)
		}
	}
	if a := r.OpenCode; a != nil && (a.Name != "culler-opencode-linux-arm64.tar.gz" || !hexDigest.MatchString(a.SHA256) || a.Size <= 0) {
		return errors.New("release OpenCode archive is invalid")
	}
	if a := r.AgentsView; a != nil && (!runtimeVersion.MatchString(a.Version) ||
		!hexDigest.MatchString(a.ArchiveSHA256) || !hexDigest.MatchString(a.ExecutableSHA256)) {
		return errors.New("release AgentsView identity is invalid")
	}
	if h := r.HostBun; h != nil {
		if err := h.validate(); err != nil {
			return err
		}
	}
	checks := map[string]bool{}
	for _, c := range r.Checks {
		if c.Name == "" || checks[c.Name] || !c.Passed {
			return errors.New("release checks are incomplete or invalid")
		}
		checks[c.Name] = true
	}
	for _, name := range RequiredChecks {
		if !checks[name] {
			return fmt.Errorf("release check %s is absent", name)
		}
	}
	return nil
}

func (h HostBunRelease) validate() error {
	if !runtimeVersion.MatchString(h.Version) {
		return errors.New("release host Bun version is invalid")
	}
	if _, ok := h.Archives[HostBunPlatform]; !ok {
		return errors.New("release host Bun archive for " + HostBunPlatform + " is absent")
	}
	for platform, archive := range h.Archives {
		if platform == "" || archive.Name == "" || strings.ContainsAny(archive.Name, "/\\\x00") ||
			!strings.HasPrefix(archive.URL, "https://") || archive.Size <= 0 || !hexDigest.MatchString(archive.SHA256) {
			return errors.New("release host Bun archive " + platform + " is invalid")
		}
	}
	return nil
}

func Decode(data []byte) (Record, error) {
	var r Record
	if len(data) > 1<<20 {
		return r, errors.New("release record exceeds its size limit")
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, errors.New("release record is invalid JSON")
	}
	return r, r.Validate()
}
func Load(path string) (Record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	return Decode(data)
}
func Save(path string, r Record) error {
	if err := r.Validate(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".release-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
