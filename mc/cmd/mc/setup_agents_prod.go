//go:build !test_fake_routing

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"

	operatorconfig "mc/config"
	"mc/deployment"
	"mc/onboarding"
	"mc/verbs"
)

type setupConnection struct {
	Binding string
	Mode    string
	Prepare func() error
	// Reported is true when the line above the failure already names the agent and the result.
	Reported bool
}

func (w *setupWizard) catalog() (operatorconfig.Config, error) {
	return operatorconfig.Load(filepath.Join(w.home, "config.toml"))
}

// setupBindingForKind prefers the reference binding of the credential kind.
func setupBindingForKind(cfg operatorconfig.Config, kind string) string {
	references, ids := []string{}, []string{}
	defaults := operatorconfig.Default().Runtimes.Bindings
	for id, binding := range cfg.Runtimes.Bindings {
		if cfg.Runtimes.Credentials[binding.Credential].Kind == kind {
			ids = append(ids, id)
			if _, reference := defaults[id]; reference {
				references = append(references, id)
			}
		}
	}
	if len(references) > 0 {
		ids = references
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

func setupHarnessKind(harness string) string {
	if harness == "codex" {
		return operatorconfig.CredentialKindCodexOAuth
	}
	return operatorconfig.CredentialKindClaudeOAuth
}

func (w *setupWizard) connectionHealthy(binding string) bool {
	if !w.session.RuntimeVerified[binding] || w.deps.runtimeReady(w.home, []string{binding}) != nil {
		return false
	}
	cfg, err := w.catalog()
	if err != nil {
		return false
	}
	spec, ok := cfg.Runtimes.Bindings[binding]
	if !ok {
		return false
	}
	identity, err := w.deps.identity(w.home, spec.Credential)
	return err == nil && identity != "" && setupAccessIdentity(cfg, binding, identity) == w.session.RuntimeIdentities[binding]
}

func setupAccessIdentity(cfg operatorconfig.Config, binding, identity string) string {
	spec := cfg.Runtimes.Bindings[binding]
	body, _ := json.Marshal(struct {
		Identity string
		Binding  operatorconfig.RuntimeBinding
		Source   operatorconfig.CredentialSource
	}{identity, spec, cfg.Runtimes.Credentials[spec.Credential]})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func (w *setupWizard) agents() error {
	return w.agentChoices(w.session.Stage)
}

func (w *setupWizard) agentChoices(returnStage int) (retErr error) {
	if stage := returnStage; stage >= 3 && stage < len(setupStageTitles) {
		w.showStageUntil(2, stage, w.agentDestination(stage))
		defer func() {
			if retErr == nil {
				w.showStage(stage)
			}
		}()
	}
	if w.session.Access != nil {
		if err := w.resumeAccess(); err != nil && !errors.Is(err, onboarding.ErrBack) {
			return err
		}
	}
	if err := w.connectFlagSelections(); err != nil {
		return err
	}
	for {
		cfg, err := w.catalog()
		if err != nil {
			return err
		}
		// An unfinished agent test requires a choice from the operator.
		if !w.agentChoiceRequired && len(w.session.RuntimeIncomplete) == 0 {
			for _, binding := range w.session.RuntimeBindings {
				if w.connectionHealthy(binding) {
					return w.selectAgent(binding)
				}
			}
		}
		choices := []onboarding.Choice{}
		for _, binding := range w.session.RuntimeBindings {
			if w.connectionHealthy(binding) {
				choices = append(choices, setupChoice("saved:"+binding,
					"Use "+setupAgentNames(cfg, []string{binding})[0], "Use the existing connection."))
			}
		}
		for _, binding := range w.session.RuntimeIncomplete {
			spec, exists := cfg.Runtimes.Bindings[binding]
			if !exists {
				continue
			}
			identity, err := w.deps.identity(w.home, spec.Credential)
			if err == nil && identity != "" {
				choices = append(choices, setupChoice("retry:"+binding, "Retry "+w.agentName(binding), "Test the saved connection."))
			}
		}
		choices = append(choices,
			setupChoice("login:codex", "Sign in to Codex", ""),
			setupChoice("login:claude-sdk", "Sign in to Claude Code", ""),
			setupChoice("api-key:codex", "Use "+operatorconfig.Default().Runtimes.Credentials["codex-api-key"].Name, "API charges apply."),
			setupChoice("api-key:claude-sdk", "Use "+operatorconfig.Default().Runtimes.Credentials["claude-api-key"].Name, "API charges apply."),
			setupChoice("api-key:opencode", "Use another provider's API key", "OpenCode runs the agent. API charges apply."),
		)
		choice, err := w.ui.Choose("Choose how to connect", choices, 0)
		if err != nil {
			return err
		}
		w.agentChoiceRequired = true
		switch {
		case strings.HasPrefix(choice, "saved:"):
			return w.selectAgent(strings.TrimPrefix(choice, "saved:"))
		case strings.HasPrefix(choice, "retry:"):
			binding := strings.TrimPrefix(choice, "retry:")
			err = w.checkConnection(setupConnection{Binding: binding, Mode: "saved"})
			if err == nil && w.connectionHealthy(binding) {
				return w.selectAgent(binding)
			}
		case strings.HasPrefix(choice, "login:"):
			err = w.subscriptionAccess(strings.TrimPrefix(choice, "login:"))
		case strings.HasPrefix(choice, "api-key:"):
			err = w.apiKeyAccess(strings.TrimPrefix(choice, "api-key:"))
		}
		if err != nil && !errors.Is(err, onboarding.ErrBack) {
			return err
		}
	}
}

func (w *setupWizard) selectAgent(binding string) error {
	w.session.RuntimeBindings = []string{binding}
	w.session.RuntimeIncomplete = nil
	w.agentChoiceRequired = false
	if err := w.recordAgentSelections(); err != nil {
		return err
	}
	if err := w.save(); err != nil {
		return err
	}
	w.ui.Panel("Your agent", []string{w.agentName(binding), "You can add more agents in the dashboard after setup."})
	return nil
}

func (w *setupWizard) agentName(binding string) string {
	if cfg, err := w.catalog(); err == nil {
		return setupAgentNames(cfg, []string{binding})[0]
	}
	return binding
}

func (w *setupWizard) agentDestination(returnStage int) string {
	if returnStage == 4 {
		return "Start Culler"
	}
	return "Review your setup"
}

func (w *setupWizard) recordAgentSelections() error {
	if len(w.session.RuntimeBindings) == 0 {
		return nil
	}
	_, err := verbs.RecordOnboardRuntimeBindings(strings.Join(w.session.RuntimeBindings, ","))
	return err
}

func (w *setupWizard) connectFlagSelections() error {
	bindings, err := selectedRuntimeBindings(w.answers.RuntimeBindings)
	if err != nil {
		return nil
	}
	cfg, err := w.catalog()
	if err != nil {
		return err
	}
	if len(bindings) > 1 {
		return &setupFailure{cause: "Setup accepts one agent. Add more agents in the dashboard after setup."}
	}
	for _, id := range bindings {
		if w.connectionHealthy(id) {
			w.session.RuntimeBindings = []string{id}
			continue
		}
		binding, ok := cfg.Runtimes.Bindings[id]
		if !ok {
			return &setupFailure{cause: "A selected agent configuration is unavailable. Check its name with your coding harness."}
		}
		if !w.answers.AcquireRuntimeAuth {
			continue
		}
		w.session.Access = &onboarding.AccessChoice{
			Mode: "login", Step: "check", Harness: binding.Harness, Template: id, Binding: id,
		}
		if err := w.resumeAccess(); err != nil {
			return err
		}
	}
	return nil
}

func (w *setupWizard) checkConnection(connection setupConnection) error {
	program := "the agent"
	if cfg, err := w.catalog(); err == nil {
		if name := map[string]string{"codex": "Codex", "claude-sdk": "Claude Code"}[cfg.Runtimes.Bindings[connection.Binding].Harness]; name != "" {
			program = name
		}
	}
	prompt := "Sign in and connect?"
	choice := setupChoice("check", "Sign in", "Sign in to "+program+" in your browser. Culler then tests the connection.")
	if connection.Mode == "api_key" || connection.Mode == "saved" {
		prompt = "Test this connection?"
		choice = setupChoice("check", "Test model", "The test uses the selected model and can incur provider charges.")
	} else if connection.Mode != "login" {
		return &setupFailure{cause: "The selected credential source is unsupported."}
	}
	if !w.answers.ConfirmLiveSpend {
		if _, err := w.ui.Choose(prompt, []onboarding.Choice{choice}, 0); err != nil {
			return err
		}
	}
	if connection.Prepare != nil {
		if err := connection.Prepare(); err != nil {
			return err
		}
	}
	activity := "Connect " + w.agentName(connection.Binding)
	if connection.Mode == "api_key" || connection.Mode == "saved" {
		activity = "Test " + w.agentName(connection.Binding)
	}
	connection.Reported = true
	wasHealthy := w.connectionHealthy(connection.Binding)
	err := w.ui.Activity(activity, func() error {
		return w.deps.connect(w, connection)
	})
	return w.finishConnection(connection, wasHealthy, err)
}

func (w *setupWizard) finishConnection(connection setupConnection, wasHealthy bool, err error) error {
	if err != nil {
		var cleanup *deployment.RuntimeAuthCleanupError
		if errors.As(err, &cleanup) {
			return cleanup
		}
		if errors.Is(err, onboarding.ErrExit) || errors.Is(err, onboarding.ErrBack) {
			return err
		}
		if !wasHealthy || !w.connectionHealthy(connection.Binding) {
			w.session.RuntimeVerified[connection.Binding] = false
			w.session.RuntimeBindings = slices.DeleteFunc(w.session.RuntimeBindings, func(id string) bool { return id == connection.Binding })
		}
		if !slices.Contains(w.session.RuntimeIncomplete, connection.Binding) {
			w.session.RuntimeIncomplete = append(w.session.RuntimeIncomplete, connection.Binding)
		}
		if saveErr := w.save(); saveErr != nil {
			return saveErr
		}
		name := w.agentName(connection.Binding)
		title, lines := name+" connection failed", []string{setupCause(err)}
		var failure *setupFailure
		if errors.As(err, &failure) && failure.diagnostic != "" {
			lines = append(lines, failure.diagnostic)
		}
		if connection.Reported {
			title = ""
		}
		// A failure without a known cause gets a diagnostic record.
		details := []string{"Diagnostic: " + setupSafeDiagnostic(err.Error())}
		if errors.As(err, &failure) {
			details = failure.details
		}
		if len(details) > 0 {
			if path, saveErr := writeSetupDiagnostic(w.home, name+" connection", nil, err); saveErr == nil {
				lines = append(lines, "Share this diagnostic record with your coding harness:", path)
			} else {
				lines = append(lines, "Setup cannot save the diagnostic record.", "Share these details with your coding harness:")
				lines = append(lines, details...)
			}
		}
		w.problem(title, lines)
		return nil
	}
	cfg, err := w.catalog()
	if err != nil {
		return err
	}
	identity, err := w.deps.identity(w.home, cfg.Runtimes.Bindings[connection.Binding].Credential)
	if err != nil {
		return err
	}
	w.session.RuntimeVerified[connection.Binding] = true
	w.session.RuntimeIncomplete = slices.DeleteFunc(w.session.RuntimeIncomplete, func(id string) bool { return id == connection.Binding })
	w.session.RuntimeIdentities[connection.Binding] = setupAccessIdentity(cfg, connection.Binding, identity)
	if !slices.Contains(w.session.RuntimeBindings, connection.Binding) {
		w.session.RuntimeBindings = append(w.session.RuntimeBindings, connection.Binding)
	}
	return w.save()
}

func setupConnect(w *setupWizard, connection setupConnection) (retErr error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer func() {
		var cleanup *deployment.RuntimeAuthCleanupError
		if errors.As(retErr, &cleanup) {
			retErr = cleanup
			return
		}
		if ctx.Err() != nil {
			retErr = onboarding.ErrExit
		}
	}()
	if ctx.Err() != nil {
		return onboarding.ErrExit
	}
	cfg, err := w.catalog()
	if err != nil {
		return err
	}
	_, ok := cfg.Runtimes.Bindings[connection.Binding]
	if !ok {
		return &setupFailure{cause: "The selected configuration is unavailable."}
	}
	if connection.Mode == "api_key" || connection.Mode == "saved" {
		verifier, err := runtimeAuthVerifier(ctx, w.home)
		if err != nil {
			return err
		}
		if err := verifier.Verify(connection.Binding, filepath.Join(w.home, "refresh-grants")); err != nil {
			source := cfg.Runtimes.Credentials[cfg.Runtimes.Bindings[connection.Binding].Credential]
			if message := deployment.OpenCodeRefusalMessage(source.CatalogEntry, err); message != "" {
				return &setupFailure{cause: message}
			}
			return setupRuntimeFailure(err, "The model test failed. The saved connection remains available.")
		}
		return nil
	}
	sources := deployment.RuntimeAuthSources{Bindings: []string{connection.Binding}, Environment: os.Environ()}
	var acquisition *deployment.RuntimeAuthAcquisition
	switch connection.Mode {
	case "login":
		login := productionRuntimeAuthLogin
		if login == nil {
			shownAddress, shownCode := "", ""
			login = deployment.NewSetupRuntimeAuthLogin(ctx, func(address, code string) {
				if address == shownAddress {
					if code != "" && code != shownCode {
						w.ui.Panel("Verification code", []string{code})
						shownCode = code
					}
					return
				}
				shownAddress, shownCode = address, code
				lines := []string{"Complete sign-in in your browser.", "If the browser does not open, use the address below."}
				if code != "" {
					lines = append(lines, "Verification code: "+code)
				}
				lines = append(lines, "Return here after the browser confirms your account.")
				w.ui.Panel("Sign in through your browser", lines)
				fmt.Fprintf(w.output, "%s\n\n", address)
			})
		}
		acquisition, err = deployment.AcquireRuntimeAuth(w.home, sources.Bindings, w.input, io.Discard, sources.Environment, login)
		if err != nil {
			return setupRuntimeFailure(err, "The cause is unknown. Sign in again, or choose another way to connect.")
		}
		defer acquisition.Cleanup()
	default:
		return &setupFailure{cause: "The selected credential source is unsupported."}
	}
	stage, err := deployment.StageRuntimeAuthSources(w.home, sources, acquisition)
	if err != nil {
		return setupRuntimeFailure(err, "Setup cannot read the credentials. Choose a way to connect.")
	}
	defer stage.Cleanup()
	if acquisition != nil {
		if err := acquisition.Cleanup(); err != nil {
			return err
		}
	}
	verifier, err := runtimeAuthVerifier(ctx, w.home)
	if err == nil {
		_, err = productionSelectedRuntimeAuthImport(w.home, stage, verifier)
	}
	if err != nil {
		return setupRuntimeFailure(err, "The cause is unknown.")
	}
	return nil
}

func setupRuntimeFailure(err error, fallback string) error {
	var cleanup *deployment.RuntimeAuthCleanupError
	if errors.As(err, &cleanup) {
		return cleanup
	}
	var proxy *deployment.ProviderProxyStartError
	if errors.As(err, &proxy) {
		return &setupFailure{cause: setupProviderProxyStartCause, details: []string{"Diagnostic: " + setupSafeDiagnostic(err.Error())}}
	}
	var replacement *deployment.APIKeyReplacementError
	if errors.As(err, &replacement) {
		return &setupFailure{cause: replacement.Error()}
	}
	var native *deployment.AuthorizationFailure
	if errors.As(err, &native) {
		return &setupFailure{cause: native.Error()}
	}
	var model *deployment.CodexSetupModelError
	if errors.As(err, &model) {
		return &setupFailure{cause: (&deployment.CodexSetupModelError{}).Error(), details: []string{"Diagnostic: " + setupSafeDiagnostic(err.Error())}}
	}
	if _, rest, found := strings.Cut(err.Error(), "forbidden ambient runtime credential "); found {
		variable, _, _ := strings.Cut(rest, " ")
		return &setupFailure{cause: "Setup cannot connect an agent while the terminal sets " + variable + ". Ask your coding harness to remove it, then run ./install.sh again in a new terminal."}
	}
	var refresh *deployment.TokenRefreshError
	if errors.As(err, &refresh) {
		cause := "The provider refused the token refresh. Sign in again, or choose another way to connect."
		switch {
		case refresh.Status == 401:
			cause = "The provider rejected the credentials. Sign in again, or choose another way to connect."
		case refresh.Status == 403:
			cause = "The provider refused access for the credentials. Sign in again, or choose another way to connect."
		case refresh.Status == 429 || refresh.Status >= 500:
			cause = "The provider could not complete the token refresh. Try again in a few minutes, or choose another way to connect."
		}
		reply := fmt.Sprintf("Provider reply: status %d", refresh.Status)
		if refresh.Code != "" {
			reply += ", error code " + refresh.Code
		}
		return &setupFailure{cause: cause, diagnostic: reply + "."}
	}
	message := strings.ToLower(err.Error())
	for _, item := range []struct{ match, cause string }{
		{"authorization executable is unavailable", "Setup cannot find the sign-in command for this agent. Install the agent, then sign in again."},
		{"exceeded two minutes", "The provider check exceeded its time limit. Check the network connection, then choose a way to connect."},
	} {
		if strings.Contains(message, item.match) {
			return &setupFailure{cause: item.cause}
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrPermission) || errors.Is(err, os.ErrNotExist) {
		return &setupFailure{cause: setupCause(err)}
	}
	return &setupFailure{cause: fallback, details: []string{"Diagnostic: " + setupSafeDiagnostic(err.Error())}}
}
