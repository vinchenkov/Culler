package deployment

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

//go:embed hostbun.json
var hostBunPinBody []byte

// HostBunPlatform is the one Mac architecture that the host Bun pin supports.
const HostBunPlatform = "darwin-arm64"

// HostBunPin is the one Bun release that a published install puts on the Mac.
// Only a change to hostbun.json changes it.
type HostBunPin struct {
	Version  string                    `json:"version"`
	Archives map[string]HostBunArchive `json:"archives"`
}

// HostBunArchive names the Bun archive of one Mac architecture.
type HostBunArchive struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

var (
	hostBunVersionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	hostBunPlatformPattern = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)
)

// LoadHostBunPin returns the validated pin that the mc binary embeds.
func LoadHostBunPin() (HostBunPin, error) {
	return ParseHostBunPin(hostBunPinBody)
}

func ParseHostBunPin(body []byte) (HostBunPin, error) {
	var pin HostBunPin
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pin); err != nil {
		return HostBunPin{}, fmt.Errorf("the host Bun pin is invalid: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return HostBunPin{}, fmt.Errorf("the host Bun pin holds data after its object")
	}
	if err := pin.validate(); err != nil {
		return HostBunPin{}, fmt.Errorf("the host Bun pin is invalid: %w", err)
	}
	return pin, nil
}

func (p HostBunPin) validate() error {
	if !hostBunVersionPattern.MatchString(p.Version) {
		return fmt.Errorf("version %q must be MAJOR.MINOR.PATCH", p.Version)
	}
	if _, ok := p.Archives[HostBunPlatform]; !ok {
		return fmt.Errorf("archives must name %s", HostBunPlatform)
	}
	for platform, archive := range p.Archives {
		if !hostBunPlatformPattern.MatchString(platform) {
			return fmt.Errorf("archive platform %q must be os-arch", platform)
		}
		if err := archive.validate(p.Version); err != nil {
			return fmt.Errorf("archive %s: %w", platform, err)
		}
	}
	return nil
}

func (a HostBunArchive) validate(version string) error {
	if a.Name == "" || a.Name == "." || a.Name == ".." || strings.ContainsAny(a.Name, "/\\\x00") || !strings.HasSuffix(a.Name, ".zip") {
		return fmt.Errorf("name %q must be a plain zip file name", a.Name)
	}
	if a.Size <= 0 {
		return fmt.Errorf("size must be positive")
	}
	if !validSHA256(a.SHA256) || strings.ToLower(a.SHA256) != a.SHA256 {
		return fmt.Errorf("sha256 must be 64 lowercase hexadecimal characters")
	}
	link, err := url.Parse(a.URL)
	if err != nil || link.Scheme != "https" || link.Host == "" || link.User != nil ||
		link.RawQuery != "" || link.ForceQuery || link.Fragment != "" ||
		!strings.HasSuffix(link.Path, "/bun-v"+version+"/"+a.Name) {
		return fmt.Errorf("url must be an https URL that ends with /bun-v%s/%s", version, a.Name)
	}
	return nil
}
