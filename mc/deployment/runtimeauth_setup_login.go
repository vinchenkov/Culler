package deployment

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

const setupRuntimeAuthLoginTimeout = 10 * time.Minute

// NewSetupRuntimeAuthLogin exposes browser authorization instructions without routine logs or credential values.
// AcquireRuntimeAuth supplies the isolated home and credential directory.
func NewSetupRuntimeAuthLogin(ctx context.Context, notify func(string, string)) RuntimeAuthLoginFunc {
	return func(binary string, _ []string, environment []string, stdin io.Reader, _ io.Writer) error {
		args, configuration := []string{"login"}, "CODEX_HOME"
		switch binary {
		case "codex":
		case "claude":
			args, configuration = []string{"auth", "login", "--claudeai"}, "CLAUDE_CONFIG_DIR"
		default:
			return fmt.Errorf("The selected subscription login is unsupported.")
		}
		home := environmentValue(environment, "HOME")
		directory := environmentValue(environment, configuration)
		if !filepath.IsAbs(home) || !filepath.IsAbs(directory) || home == directory {
			return fmt.Errorf("The subscription login requires an isolated home and credential directory.")
		}
		if err := validateRuntimeAuthEnvironment(environment); err != nil {
			return err
		}
		environment = appendLoginHome(isolatedLoginBaseEnvironment(environment), home, configuration, directory)
		bounded, cancel := context.WithTimeout(ctx, setupRuntimeAuthLoginTimeout)
		defer cancel()
		callback := notify
		if callback == nil {
			callback = func(string, string) {}
		}
		output := &setupRuntimeAuthorizationOutput{parser: agentAuthorizationOutput{notify: callback}}
		err := agentProfileCommand(bounded, binary, args, environment, stdin, output)
		output.flush()
		return err
	}
}

var setupRuntimeAuthorizationCodeLine = regexp.MustCompile(`^(?:(?i:code|device code|verification code|one-time code):\s*)?[A-Z0-9]{4}-[A-Z0-9]{4}$`)

type setupRuntimeAuthorizationOutput struct {
	mu      sync.Mutex
	parser  agentAuthorizationOutput
	line    []byte
	discard bool
}

func (output *setupRuntimeAuthorizationOutput) Write(body []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	for _, value := range body {
		if value == '\n' || value == '\r' {
			output.completeLine()
			continue
		}
		if output.discard {
			continue
		}
		if len(output.line) == 16384 {
			clear(output.line)
			output.line = output.line[:0]
			output.discard = true
			continue
		}
		output.line = append(output.line, value)
	}
	return len(body), nil
}

func (output *setupRuntimeAuthorizationOutput) flush() {
	output.mu.Lock()
	defer output.mu.Unlock()
	output.completeLine()
}

func (output *setupRuntimeAuthorizationOutput) completeLine() {
	if !output.discard {
		line := agentAuthorizationANSI.ReplaceAllString(string(output.line), "")
		for _, candidate := range agentAuthorizationURL.FindAllString(line, -1) {
			candidate = strings.TrimRight(candidate, ".,);]")
			if safeSetupAuthorizationURL(candidate) {
				_, _ = io.WriteString(&output.parser, candidate+"\n")
			}
		}
		line = strings.TrimSpace(line)
		if setupRuntimeAuthorizationCodeLine.MatchString(line) {
			_, _ = io.WriteString(&output.parser, line+"\n")
		}
	}
	clear(output.line)
	output.line = output.line[:0]
	output.discard = false
}

func safeSetupAuthorizationURL(address string) bool {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.Fragment != "" || parsed.Opaque != "" || strings.IndexFunc(address, unicode.IsControl) >= 0 {
		return false
	}
	switch strings.ToLower(filepath.Base(strings.TrimRight(parsed.Path, "/"))) {
	case "auth", "authorize", "activate", "device":
	default:
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for name, values := range query {
		switch name {
		case "client_id", "response_type", "scope", "state", "code_challenge", "code_challenge_method",
			"audience", "prompt", "login_hint", "login_method", "screen_hint", "organization", "organization_id",
			"orgUUID", "resource", "user_code", "nonce", "response_mode",
			"id_token_add_organizations", "codex_cli_simplified_flow", "originator":
		case "code":
			if len(values) != 1 || values[0] != "true" {
				return false
			}
		case "redirect_uri":
			for _, value := range values {
				redirect, err := url.Parse(value)
				if err != nil || (redirect.Scheme != "http" && redirect.Scheme != "https") || redirect.Hostname() == "" ||
					redirect.User != nil || redirect.RawQuery != "" || redirect.Fragment != "" ||
					strings.IndexFunc(value, unicode.IsControl) >= 0 {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}
