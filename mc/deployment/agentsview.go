package deployment

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

//go:embed agentsview.json
var agentsViewPinBody []byte

//go:embed agentsview.linux-amd64.json
var agentsViewLinuxAMD64PinBody []byte

//go:embed agentsview.linux-arm64.json
var agentsViewLinuxARM64PinBody []byte

// agentsViewPinBodies maps each platform mc runs AgentsView on to its
// embedded pin file. Every architecture of the two supported host families
// carries a pin, so supervision renders on any host mc installs to; a
// platform without a file fails closed at load, not at download.
var agentsViewPinBodies = map[string][]byte{
	"darwin-arm64": agentsViewPinBody,
	"linux-amd64":  agentsViewLinuxAMD64PinBody,
	"linux-arm64":  agentsViewLinuxARM64PinBody,
}

// AgentsViewPin is the one AgentsView release that each install path installs.
// Only a change to agentsview.json changes it.
type AgentsViewPin struct {
	Version        string               `json:"version"`
	Platform       string               `json:"platform"`
	Archive        AgentsViewArchive    `json:"archive"`
	Executable     AgentsViewExecutable `json:"executable"`
	DisabledAgents []string             `json:"disabled_agents"`
}

type AgentsViewArchive struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type AgentsViewExecutable struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

var (
	agentsViewVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	agentsViewAgentPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// LoadAgentsViewPin returns the validated pin of the platform this binary
// runs on.
func LoadAgentsViewPin() (AgentsViewPin, error) {
	return LoadAgentsViewPinForPlatform(runtime.GOOS + "-" + runtime.GOARCH)
}

// LoadAgentsViewPinForPlatform returns the validated pin of a named published
// platform, for tools that build or verify a release other than the host's.
func LoadAgentsViewPinForPlatform(platform string) (AgentsViewPin, error) {
	body, ok := agentsViewPinBodies[platform]
	if !ok {
		return AgentsViewPin{}, fmt.Errorf("no AgentsView pin is published for platform %s", platform)
	}
	pin, err := ParseAgentsViewPin(body)
	if err != nil {
		return AgentsViewPin{}, err
	}
	if pin.Platform != platform {
		return AgentsViewPin{}, fmt.Errorf("the AgentsView pin of %s names platform %s", platform, pin.Platform)
	}
	return pin, nil
}

func ParseAgentsViewPin(body []byte) (AgentsViewPin, error) {
	var pin AgentsViewPin
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pin); err != nil {
		return AgentsViewPin{}, fmt.Errorf("the AgentsView pin is invalid: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return AgentsViewPin{}, fmt.Errorf("the AgentsView pin holds data after its object")
	}
	if err := pin.validate(); err != nil {
		return AgentsViewPin{}, fmt.Errorf("the AgentsView pin is invalid: %w", err)
	}
	return pin, nil
}

func (p AgentsViewPin) validate() error {
	if !agentsViewVersionPattern.MatchString(p.Version) {
		return fmt.Errorf("version %q must be MAJOR.MINOR.PATCH", p.Version)
	}
	if p.Platform != "darwin-arm64" && p.Platform != "linux-amd64" && p.Platform != "linux-arm64" {
		return fmt.Errorf("platform %q must be a supported platform", p.Platform)
	}
	for _, file := range []struct {
		kind, name, sha256 string
		size               int64
	}{
		{"archive", p.Archive.Name, p.Archive.SHA256, p.Archive.Size},
		{"executable", p.Executable.Name, p.Executable.SHA256, p.Executable.Size},
	} {
		if file.name == "" || file.name == "." || file.name == ".." || strings.ContainsAny(file.name, "/\\\x00") {
			return fmt.Errorf("%s name %q must be a plain file name", file.kind, file.name)
		}
		if file.size <= 0 {
			return fmt.Errorf("%s size must be positive", file.kind)
		}
		if !validSHA256(file.sha256) || strings.ToLower(file.sha256) != file.sha256 {
			return fmt.Errorf("%s sha256 must be 64 lowercase hexadecimal characters", file.kind)
		}
	}
	link, err := url.Parse(p.Archive.URL)
	if err != nil || link.Scheme != "https" || link.Host == "" || link.User != nil ||
		link.RawQuery != "" || link.ForceQuery || link.Fragment != "" ||
		!strings.HasSuffix(link.Path, "/v"+p.Version+"/"+p.Archive.Name) {
		return fmt.Errorf("archive url must be an https URL that ends with /v%s/%s", p.Version, p.Archive.Name)
	}
	if len(p.DisabledAgents) == 0 {
		return fmt.Errorf("disabled_agents must name at least one agent")
	}
	seen := map[string]bool{}
	for _, agent := range p.DisabledAgents {
		if !agentsViewAgentPattern.MatchString(agent) {
			return fmt.Errorf("disabled_agents contains the invalid agent ID %q", agent)
		}
		if seen[agent] {
			return fmt.Errorf("disabled_agents contains %q more than once", agent)
		}
		if agent == "claude" || agent == "codex" || agent == "opencode" {
			return fmt.Errorf("disabled_agents must not contain %q, because AgentsView reads its trace copies", agent)
		}
		seen[agent] = true
	}
	return nil
}

// AgentsViewPaths holds the MC_HOME paths of AgentsView and its trace copies.
type AgentsViewPaths struct {
	Executable string
	Root       string
	// Data is the value of AGENTSVIEW_DATA_DIR.
	Data     string
	Config   string
	DebugLog string
	// Token holds the bearer token that AgentsView requires for each API
	// request. It is outside Data, so a loss of the archive keeps it.
	Token string
	// Home is the value of HOME for AgentsView. It stays empty.
	Home string
	// ClaudeTraceRoot and CodexTraceRoot are the folders that AgentsView
	// scans. The Codex trace copies keep their dated paths below
	// CodexTraceRoot.
	ClaudeTraceRoot   string
	ClaudeTraceCopies string
	CodexTraceRoot    string
	OpenCodeTraceRoot string
	StdoutLog         string
	StderrLog         string
	Health            string
}

func AgentsViewPathsFor(home string) AgentsViewPaths {
	root := filepath.Join(home, "agentsview")
	data := filepath.Join(root, "data")
	claude := filepath.Join(root, "traces", "claude", "projects")
	return AgentsViewPaths{
		Executable:        filepath.Join(home, "bin", "agentsview"),
		Root:              root,
		Data:              data,
		Config:            filepath.Join(data, "config.toml"),
		DebugLog:          filepath.Join(data, "debug.log"),
		Token:             filepath.Join(root, "token"),
		Home:              filepath.Join(root, "home"),
		ClaudeTraceRoot:   claude,
		ClaudeTraceCopies: filepath.Join(claude, "culler"),
		CodexTraceRoot:    filepath.Join(root, "traces", "codex", "sessions"),
		OpenCodeTraceRoot: filepath.Join(root, "traces", "opencode"),
		StdoutLog:         filepath.Join(home, "logs", "agentsview.stdout.log"),
		StderrLog:         filepath.Join(home, "logs", "agentsview.stderr.log"),
		Health:            filepath.Join(home, "health", "agentsview.json"),
	}
}

// WikiExportRoot is the folder that holds one wiki export folder for each
// Project.
func WikiExportRoot(home string) string {
	return filepath.Join(home, "wiki")
}
