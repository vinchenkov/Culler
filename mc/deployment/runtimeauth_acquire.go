package deployment

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type RuntimeAuthLoginFunc func(binary string, args, environment []string, stdin io.Reader, log io.Writer) error

type RuntimeAuthAcquisition struct {
	CodexAuthFile         string
	ClaudeCredentialsFile string
	sourceRoot            string
	flowRoot              string
	disposable            *runtimeAuthDisposableRoot
	sourceBoundary        *runtimeAuthSourceBoundary
	codexAuthBody         []byte
	claudeCredentialsBody []byte
}

func defaultRuntimeAuthLogin(binary string, args, environment []string, stdin io.Reader, log io.Writer) error {
	path, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("provider login executable %q is unavailable: %w", binary, err)
	}
	cmd := exec.Command(path, args...)
	cmd.Env = environment
	cmd.Stdin = stdin
	if log == nil {
		log = io.Discard
	}
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("provider-owned %s login failed: %w", binary, err)
	}
	return nil
}

func isolatedLoginBaseEnvironment(base []string) []string {
	allowed := map[string]bool{
		"ALL_PROXY": true, "HTTP_PROXY": true, "HTTPS_PROXY": true,
		"NO_PROXY": true, "all_proxy": true, "http_proxy": true,
		"https_proxy": true, "no_proxy": true,
		"LANG": true, "LC_ALL": true, "LC_CTYPE": true,
		"NODE_EXTRA_CA_CERTS": true, "PATH": true,
		"SSL_CERT_DIR": true, "SSL_CERT_FILE": true,
		"TERM": true, "TMPDIR": true, "TMP": true, "TEMP": true,
	}
	out := make([]string, 0, len(allowed)+2)
	for _, entry := range base {
		name, _, ok := strings.Cut(entry, "=")
		if ok && (allowed[name] || strings.HasPrefix(name, "LC_")) {
			out = append(out, entry)
		}
	}
	return out
}

func appendLoginHome(base []string, home, configName, configPath string) []string {
	out := append([]string(nil), base...)
	out = append(out, "HOME="+home, configName+"="+configPath)
	return out
}

func runRuntimeAuthLoginWithSealedPath(
	acquisition *RuntimeAuthAcquisition,
	bindingRoot string,
	run func() error,
) (retErr error) {
	if err := acquisition.disposable.requireExpectedPath(
		acquisition.sourceRoot, "runtime-auth acquisition flow"); err != nil {
		return err
	}
	bindingFD, err := unix.Openat(
		acquisition.disposable.rootFD, bindingRoot,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open isolated %s login root: %w", bindingRoot, err)
	}
	if _, _, err := runtimeAuthDirectoryIdentity(
		bindingFD, "isolated "+bindingRoot+" login root"); err != nil {
		unix.Close(bindingFD)
		return err
	}
	sealed := []struct {
		fd    int
		label string
	}{
		{acquisition.disposable.homeFD, "deployment credential directory"},
		{acquisition.disposable.parentFD, "runtime-auth source root"},
		{acquisition.disposable.rootFD, "runtime-auth acquisition flow"},
		{bindingFD, "isolated " + bindingRoot + " login root"},
	}
	for i, directory := range sealed {
		if err := unix.Fchmod(directory.fd, 0o500); err != nil {
			for j := i - 1; j >= 0; j-- {
				unix.Fchmod(sealed[j].fd, 0o700)
			}
			unix.Close(bindingFD)
			return fmt.Errorf("seal %s during provider login: %w", directory.label, err)
		}
	}
	defer func() {
		var restoreErrs []error
		for i := len(sealed) - 1; i >= 0; i-- {
			if err := unix.Fchmod(sealed[i].fd, 0o700); err != nil {
				restoreErrs = append(restoreErrs,
					fmt.Errorf("restore %s after provider login: %w", sealed[i].label, err))
			}
		}
		if err := unix.Close(bindingFD); err != nil {
			restoreErrs = append(restoreErrs, err)
		}
		if err := acquisition.disposable.requireExpectedPath(
			acquisition.sourceRoot, "runtime-auth acquisition flow"); err != nil {
			restoreErrs = append(restoreErrs, err)
		}
		retErr = errors.Join(retErr, errors.Join(restoreErrs...))
	}()
	return run()
}

func (a *RuntimeAuthAcquisition) Cleanup() error {
	if a == nil || a.disposable == nil {
		return nil
	}
	var replacementErr error
	if a.sourceBoundary != nil && a.disposable.requireExpectedPath(a.sourceRoot, "runtime-auth acquisition flow") != nil {
		replacementErr = a.sourceBoundary.removeCredentialFiles()
	}
	err := errors.Join(replacementErr, a.disposable.Cleanup("runtime-auth acquisition flow"))
	if a.sourceBoundary != nil {
		err = errors.Join(err, unix.Close(a.sourceBoundary.fd))
		a.sourceBoundary = nil
	}
	a.disposable = nil
	if err == nil {
		a.flowRoot = ""
	}
	return err
}

// AcquireRuntimeAuth runs only provider-owned subscription login commands in
// private host directories outside deployment mounts. It never reads or copies ~/.codex,
// ~/.claude, API keys, or provider endpoint overrides.
func AcquireRuntimeAuth(home string, bindings []string, stdin io.Reader, log io.Writer, environment []string, login RuntimeAuthLoginFunc) (result *RuntimeAuthAcquisition, retErr error) {
	canonicalHome, err := CanonicalHome(home)
	if err != nil {
		return nil, fmt.Errorf("resolve runtime-auth acquisition MC_HOME: %w", err)
	}
	if err := requireOwnerOnlyDirectory(canonicalHome, "MC_HOME"); err != nil {
		return nil, err
	}
	catalog, err := LoadRuntimeCatalog(canonicalHome)
	if err != nil {
		return nil, err
	}
	bindings, err = validateRuntimeBindings(catalog, bindings)
	if err != nil {
		return nil, err
	}
	if err := validateRuntimeAuthEnvironment(environment); err != nil {
		return nil, err
	}
	wantsCodex, wantsClaude, err := catalog.OAuthKindsOf(bindings)
	if err != nil {
		return nil, err
	}
	if !wantsCodex && !wantsClaude {
		return nil, fmt.Errorf("runtime-auth acquisition requires at least one OAuth subscription binding")
	}
	sourceRoot, err := prepareRuntimeAuthSourceRoot(canonicalHome)
	if err != nil {
		return nil, err
	}
	if err := requireOwnerOnlyDirectory(sourceRoot, "runtime-auth source root"); err != nil {
		return nil, err
	}
	flowRoot, err := os.MkdirTemp(sourceRoot, ".provider-login-")
	if err != nil {
		return nil, fmt.Errorf("create isolated provider-login flow: %w", err)
	}
	disposable, err := openRuntimeAuthDisposableRoot(
		sourceRoot, flowRoot, ".provider-login-", "runtime-auth acquisition flow")
	if err != nil {
		removeErr := os.Remove(flowRoot)
		return nil, errors.Join(err, removeErr)
	}
	acquisition := &RuntimeAuthAcquisition{
		sourceRoot: sourceRoot, flowRoot: flowRoot, disposable: disposable,
	}
	success := false
	defer func() {
		if !success {
			if cleanupErr := acquisition.Cleanup(); cleanupErr != nil {
				if retErr != nil {
					retErr = fmt.Errorf("%v; isolated provider-login cleanup also failed: %w", retErr, cleanupErr)
				} else {
					retErr = cleanupErr
				}
			}
		}
	}()
	acquisition.sourceBoundary, err = retainRuntimeAuthSourceBoundary(flowRoot)
	if err != nil {
		return nil, err
	}
	base := isolatedLoginBaseEnvironment(environment)
	if login == nil {
		login = defaultRuntimeAuthLogin
	}
	if wantsCodex {
		if err := acquisition.disposable.requireExpectedPath(
			sourceRoot, "runtime-auth acquisition flow"); err != nil {
			return nil, err
		}
		flowHome := filepath.Join(flowRoot, "chatgpt", "home")
		config := filepath.Join(flowRoot, "chatgpt", "codex")
		if err := os.MkdirAll(flowHome, 0o700); err != nil {
			return nil, fmt.Errorf("create isolated Codex home: %w", err)
		}
		if err := os.Mkdir(config, 0o700); err != nil {
			return nil, fmt.Errorf("create isolated Codex config: %w", err)
		}
		if err := runRuntimeAuthLoginWithSealedPath(acquisition, "chatgpt", func() error {
			return login("codex", []string{"login"},
				appendLoginHome(base, flowHome, "CODEX_HOME", config), stdin, log)
		}); err != nil {
			return nil, err
		}
		acquisition.CodexAuthFile = filepath.Join(config, "auth.json")
		acquisition.codexAuthBody, err = readRuntimeAuthSecretAt(
			acquisition.disposable.rootFD,
			[]string{"chatgpt", "codex"}, "auth.json", "acquired Codex auth")
		if err != nil {
			return nil, err
		}
	}
	if wantsClaude {
		if err := acquisition.disposable.requireExpectedPath(
			sourceRoot, "runtime-auth acquisition flow"); err != nil {
			return nil, err
		}
		flowHome := filepath.Join(flowRoot, "claude", "home")
		config := filepath.Join(flowRoot, "claude", "config")
		if err := os.MkdirAll(flowHome, 0o700); err != nil {
			return nil, fmt.Errorf("create isolated Claude home: %w", err)
		}
		if err := os.Mkdir(config, 0o700); err != nil {
			return nil, fmt.Errorf("create isolated Claude config: %w", err)
		}
		loginEnvironment := appendLoginHome(base, flowHome, "CLAUDE_CONFIG_DIR", config)
		// Built before the browser flow so an unusable credential store costs
		// no login. On darwin the CLI stores only to a keychain, so without
		// this the login reports success and leaves nothing behind.
		store, err := prepareClaudeCredentialStore(flowHome, loginEnvironment)
		if err != nil {
			return nil, err
		}
		if err := runRuntimeAuthLoginWithSealedPath(acquisition, "claude", func() error {
			return login("claude", []string{"auth", "login", "--claudeai"},
				loginEnvironment, stdin, log)
		}); err != nil {
			return nil, err
		}
		if err := store.export(config); err != nil {
			return nil, err
		}
		acquisition.ClaudeCredentialsFile = filepath.Join(config, ".credentials.json")
		acquisition.claudeCredentialsBody, err = readRuntimeAuthSecretAt(
			acquisition.disposable.rootFD,
			[]string{"claude", "config"}, ".credentials.json",
			"acquired Claude credentials")
		if err != nil {
			return nil, err
		}
	}
	if err := unix.Fsync(acquisition.disposable.rootFD); err != nil {
		return nil, fmt.Errorf("sync isolated provider-login flow: %w", err)
	}
	success = true
	return acquisition, nil
}
