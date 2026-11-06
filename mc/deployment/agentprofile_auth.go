package deployment

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	operatorconfig "mc/config"
	"mc/routing"
)

func AgentProfileAuthorizationCapability(kind string) (bool, string) {
	if agentAPIKeyKind(kind) {
		return true, ""
	}
	binary := ""
	switch kind {
	case operatorconfig.CredentialKindCodexOAuth:
		binary = "codex"
	case operatorconfig.CredentialKindClaudeOAuth:
		binary = "claude"
	default:
		return false, "Select a supported sign-in integration."
	}
	if _, err := authorizationExecutable(binary); err != nil {
		return false, "The host lacks the subscription authorization executable. Repair the installation with your coding harness, then retry."
	}
	return true, ""
}

// The supervised PATH holds only the mc, bun, and docker directories. The
// provider executable can live elsewhere, so the lookup continues through the
// usual install locations on the Mac.
func authorizationExecutable(binary string) (string, error) {
	if path, err := exec.LookPath(binary); err == nil {
		return path, nil
	}
	harness := "codex"
	if binary == "claude" {
		harness = "claude-sdk"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	path, err := findAgentTool(harness, home, "")
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", exec.ErrNotFound
	}
	return path, nil
}

var agentAuthorizationURL = regexp.MustCompile(`https://[^\s<>"\x1b]+`)
var agentAuthorizationCode = regexp.MustCompile(`\b[A-Z0-9]{4}-[A-Z0-9]{4}\b`)
var agentAuthorizationANSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

type agentAuthorizationOutput struct {
	mu       sync.Mutex
	body     string
	notify   func(string, string)
	lastURL  string
	lastCode string
}

func (w *agentAuthorizationOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.body += string(p)
	if len(w.body) > 16384 {
		w.body = w.body[len(w.body)-16384:]
	}
	body := agentAuthorizationANSI.ReplaceAllString(w.body, "")
	selected := w.lastURL
	for _, candidate := range agentAuthorizationURL.FindAllString(body, -1) {
		candidate = strings.TrimRight(candidate, ".,);]")
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.Hostname() == "" || parsed.User != nil {
			continue
		}
		path := strings.ToLower(parsed.Path)
		if !strings.Contains(path, "auth") && !strings.Contains(path, "activate") && !strings.Contains(path, "device") {
			continue
		}
		query := parsed.Query()
		if query.Has("access_token") || query.Has("refresh_token") || query.Has("id_token") {
			continue
		}
		selected = candidate
	}
	code := agentAuthorizationCode.FindString(body)
	if code == "" {
		code = w.lastCode
	}
	if selected != "" && (selected != w.lastURL || code != w.lastCode) {
		w.lastURL = selected
		w.lastCode = code
		w.notify(selected, code)
	}
	return len(p), nil
}

func agentProfileCommand(ctx context.Context, binary string, args, environment []string, stdin io.Reader, output io.Writer) error {
	path, err := authorizationExecutable(binary)
	if err != nil {
		return fmt.Errorf("The authorization executable is unavailable.")
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = environment
	cmd.Stdin = stdin
	diagnostic := &authorizationFailureOutput{target: output}
	cmd.Stdout = diagnostic
	cmd.Stderr = diagnostic
	cmd.WaitDelay = 100 * time.Millisecond
	// A separate session lets the login read stdin without a background terminal suspension.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		exitCode := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			exitCode = exit.ExitCode()
		}
		return diagnostic.failure(exitCode)
	}
	return nil
}

func authorizeAgentProfileConnection(ctx context.Context, home, id string, notify func(string, string)) (any, error) {
	catalog, err := LoadRuntimeCatalog(home)
	if err != nil {
		return nil, err
	}
	source, ok := catalog.Credentials[id]
	if !ok {
		return nil, fmt.Errorf("The connection is unavailable.")
	}
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return nil, err
	}
	scratch, err := os.MkdirTemp(root, ".authorization-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(scratch)
	cfg, err := operatorconfig.Load(filepath.Join(home, "config.toml"))
	if err != nil {
		return nil, err
	}
	cfg.Runtimes.Credentials = map[string]operatorconfig.CredentialSource{id: source}
	cfg.Runtimes.Bindings = map[string]operatorconfig.RuntimeBinding{"authorization": {Harness: source.Harness(), Credential: id, Model: "authorization"}}
	cfg.Runtimes.EnabledBindings = []string{"authorization"}
	if _, err = operatorconfig.WriteAtomic(filepath.Join(scratch, "config.toml"), cfg); err != nil {
		return nil, err
	}
	output := &agentAuthorizationOutput{notify: notify}
	acquisition, err := AcquireRuntimeAuth(scratch, []string{"authorization"}, nil, output, isolatedLoginBaseEnvironment(os.Environ()), func(binary string, args, environment []string, stdin io.Reader, log io.Writer) error {
		return agentProfileCommand(ctx, binary, args, environment, stdin, log)
	})
	if err != nil {
		return nil, nativeCredentialFailure(err)
	}
	defer acquisition.Cleanup()
	var body []byte
	if source.Harness() == "codex" {
		body = acquisition.codexAuthBody
	} else {
		body = acquisition.claudeCredentialsBody
	}
	grant, err := buildRuntimeGrantBody(id, source, body)
	if err != nil {
		var native *AuthorizationFailure
		if errors.As(err, &native) {
			return nil, native
		}
		return nil, &AuthorizationFailure{Code: "credential_invalid", ExitCode: -1}
	}
	return grant, nil
}

func testAgentProfileModel(ctx context.Context, home, image string, catalog RuntimeCatalog, binding string) (LiveProviderObservation, error) {
	if catalog.Bindings[binding].Harness == "opencode" {
		for {
			status := OpenCodeDownloadStatus(home)
			if status == nil {
				return LiveProviderObservation{}, fmt.Errorf("Download OpenCode before the model test.")
			}
			if status.Status == "succeeded" {
				break
			}
			if status.Status == "failed" {
				return LiveProviderObservation{}, fmt.Errorf("%s Retry the OpenCode download.", status.Message)
			}
			select {
			case <-ctx.Done():
				return LiveProviderObservation{}, ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}

	spec := catalog.Bindings[binding]
	if spec.Effort != "" {
		models, err := AgentConnectionModels(home, AgentModelListRequest{ConnectionID: spec.Credential})
		if err != nil {
			return LiveProviderObservation{}, err
		}
		if err := validateAgentModelEffort(models.Models, spec.Model, spec.Effort); err != nil {
			return LiveProviderObservation{}, err
		}
	}
	verifier := NewAdapterNoopVerifier(home, image)
	verifier.Catalog = catalog
	verifier.Context = ctx
	connection := catalog.Bindings[binding].Credential
	identity, err := agentProfileAuthorizationID(home, catalog, connection)
	if err != nil {
		return LiveProviderObservation{}, err
	}
	root, err := agentProfileOperationsRoot(home)
	if err != nil {
		return LiveProviderObservation{}, err
	}
	stage, err := os.MkdirTemp(root, ".model-credentials-")
	if err != nil {
		return LiveProviderObservation{}, err
	}
	defer os.RemoveAll(stage)
	verifier.Access = func(id, profile, model string, maximum int) (*providerAccessLease, error) {
		current, err := AgentConnectionAuthorizationIdentity(home, id)
		if err != nil || current != identity {
			return nil, fmt.Errorf("The connection authorization changed. Retry the model test.")
		}
		return authorizeProviderAccessContext(ctx, home, id, profile, model, "", maximum, routing.ProfileRevision(catalog.Bindings[binding]))
	}
	verifier.Run = func(args []string, stdin string) (runtimeAuthOutput, error) {
		var result runtimeAuthOutput
		current, err := agentProfileAuthorizationID(home, catalog, connection)
		if err != nil || current != identity {
			return result, fmt.Errorf("The connection changed before the model test.")
		}
		nameBytes, err := residentAcceptanceNonce()
		if err != nil {
			return result, err
		}
		name := "mc-profile-test-" + nameBytes
		args = append([]string{args[0], "--name", name}, args[1:]...)
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(cleanup, "docker", "rm", "-f", name)
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			_ = cmd.Run()
		}()
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stdin = strings.NewReader(stdin)
		stdout := &boundedRuntimeAuthBuffer{limit: 16 * 1024 * 1024}
		stderr := &boundedRuntimeAuthBuffer{limit: 16 * 1024 * 1024}
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		err = cmd.Run()
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if stdout.overflow || stderr.overflow {
			return result, fmt.Errorf("The harness output exceeded its limit.")
		}
		result.Stdout, result.Stderr = stdout.body, stderr.body
		return result, err
	}
	observation, err := verifier.VerifyModel(binding, stage)
	observation.ProfileAuthorizationID = identity
	return observation, err
}
