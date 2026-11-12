//go:build !test_fake_routing

package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	operatorconfig "mc/config"
	"mc/onboarding"
)

var (
	// errSetupAccessNameTaken reports an account name that Culler took for another use after setup chose it.
	errSetupAccessNameTaken = errors.New("the access name is in use")
)

func (w *setupWizard) subscriptionAccess(harness string) error {
	if w.session.Access == nil {
		choice, err := w.subscriptionChoice(harness)
		if err != nil {
			return err
		}
		w.session.Access = choice
	}
	return w.resumeAccess()
}

func (w *setupWizard) subscriptionChoice(harness string) (*onboarding.AccessChoice, error) {
	cfg, err := w.catalog()
	if err != nil {
		return nil, err
	}
	binding := setupBindingForKind(cfg, setupHarnessKind(harness))
	if binding == "" {
		return nil, &setupFailure{cause: "The selected subscription configuration is unavailable."}
	}
	choice := &onboarding.AccessChoice{Mode: "login", Step: "check", Harness: harness, Template: binding, Binding: binding}
	identity, err := w.deps.identity(w.home, cfg.Runtimes.Bindings[binding].Credential)
	if err != nil {
		return nil, err
	}
	if identity != "" {
		choice.NewAccount = true
		choice.Name = setupAccountName(cfg, harness)
		// An untested account of this setup gives its name to the new account.
		for number := 2; fmt.Sprintf("%s-%d", harness, number) != choice.Name; number++ {
			name := fmt.Sprintf("%s-%d", harness, number)
			if used, err := w.connectionUsed(name); err == nil && !used && slices.Contains(w.session.RuntimeCreated, name) {
				choice.Name, choice.CreatedBinding = name, name
				break
			}
		}
		choice.Binding = choice.Name
	}
	return choice, nil
}

// setupAccountName names an additional account after its harness.
func setupAccountName(cfg operatorconfig.Config, harness string) string {
	for number := 2; ; number++ {
		if name := fmt.Sprintf("%s-%d", harness, number); setupAccessNameAvailable(cfg, name, "") {
			return name
		}
	}
}

func (w *setupWizard) ask(prompt onboarding.Prompt) (string, error) {
	if terminal, ok := w.ui.(interface {
		Ask(onboarding.Prompt) (string, error)
	}); ok {
		return terminal.Ask(prompt)
	}
	correction := prompt.Correction
	for {
		if correction != nil {
			w.problem(correction.Title, correction.Lines)
		}
		answer, err := w.ui.Input(prompt.Label, prompt.Value, prompt.Secret)
		if err != nil || prompt.Check == nil {
			return answer, err
		}
		if correction = prompt.Check(answer); correction == nil {
			return answer, nil
		}
	}
}

// search falls back to a query and a list of matches when the setup interface cannot search.
func (w *setupWizard) search(search onboarding.Search) (string, error) {
	if terminal, ok := w.ui.(interface {
		Search(onboarding.Search) (string, error)
	}); ok {
		return terminal.Search(search)
	}
	for {
		query, err := w.ask(onboarding.Prompt{Label: search.Label + " search", Hints: append(slices.Clone(search.Hints), search.QueryHints...)})
		if err != nil {
			return "", err
		}
		choices := search.Find(query)
		if len(choices) == 0 {
			w.problem(search.Empty.Title, search.Empty.Lines)
			continue
		}
		if value, err := w.ui.Choose(search.Choose, choices, 0); !errors.Is(err, onboarding.ErrBack) {
			return value, err
		}
	}
}

// setupCorrection names a rejected answer and its problems.
func setupCorrection(title, answer string, problems ...string) *onboarding.Correction {
	lines := slices.Clone(problems)
	if answer != "" {
		if runes := []rune(answer); len(runes) > 100 {
			answer = string(runes[:100]) + "…"
		}
		lines = append([]string{"You entered: " + answer}, lines...)
	}
	return &onboarding.Correction{Title: title, Lines: lines}
}

func (w *setupWizard) resumeAccess() error {
	for w.session.Access != nil {
		choice := w.session.Access
		if err := w.save(); err != nil {
			return err
		}
		var err error
		if choice.Mode == "api_key" && choice.Step != "check" {
			err = w.apiKeyStep(choice)
		} else {
			// A saved setup record can hold the step of a name prompt. Setup names the account itself.
			choice.Step = "check"
			if choice.NewAccount {
				choice.Binding = choice.Name
			}
			connection := setupConnection{Binding: choice.Binding, Mode: choice.Mode}
			if choice.Mode == "login" {
				connection.Prepare = func() error { return w.prepareAccessChoice(choice) }
			}
			err = w.checkConnection(connection)
			if errors.Is(err, errSetupAccessNameTaken) {
				cfg, catalogErr := w.catalog()
				if catalogErr != nil {
					return catalogErr
				}
				choice.Name, choice.CreatedBinding = setupAccountName(cfg, choice.Harness), ""
				continue
			}
			if err == nil {
				if w.connectionHealthy(choice.Binding) && !slices.Contains(w.session.RuntimeIncomplete, choice.Binding) {
					w.session.RuntimeBindings = []string{choice.Binding}
					w.session.RuntimeIncomplete = nil
					w.agentChoiceRequired = false
				}
				w.session.Access = nil
				return w.save()
			}
		}
		if errors.Is(err, onboarding.ErrBack) {
			if !setupAccessBack(choice) {
				w.session.Access = nil
				if saveErr := w.save(); saveErr != nil {
					return saveErr
				}
				return onboarding.ErrBack
			}
			continue
		}
		if err != nil {
			return err
		}
	}
	return w.save()
}

func (w *setupWizard) operatorPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(w.operatorHome, path[2:])
	}
	return path
}

func setupAccessBack(choice *onboarding.AccessChoice) bool {
	if choice.Mode == "api_key" {
		switch choice.Step {
		case "check":
			choice.Step = "model"
		case "download":
			choice.Step = "effort"
		case "effort":
			choice.Step = "model"
		case "model":
			choice.Step = "key"
		case "key":
			if strings.HasPrefix(choice.Template, "opencode:") {
				choice.Step = "provider"
			} else {
				choice.Step = ""
			}
		default:
			choice.Step = ""
		}
		return choice.Step != ""
	}
	choice.Step = ""
	return false
}

var setupAccessName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func setupAccessNameAvailable(cfg operatorconfig.Config, name, created string) bool {
	if !setupAccessName.MatchString(name) {
		return false
	}
	_, bindingExists := cfg.Runtimes.Bindings[name]
	_, credentialExists := cfg.Runtimes.Credentials[name]
	return name == created || (!bindingExists && !credentialExists)
}

func setupModelProblems(model string) []string {
	if model == "" {
		return []string{"The model identifier is empty."}
	}
	problems := []string{}
	if strings.Contains(model, ",") {
		problems = append(problems, "Enter one model identifier without a comma.")
	}
	if strings.TrimSpace(model) != model {
		problems = append(problems, "Remove the space at the start or the end.")
	}
	if len(model) > 256 {
		problems = append(problems, "Use a maximum of 256 characters.")
	}
	if len(problems) == 0 && (!utf8.ValidString(model) || strings.IndexFunc(model, unicode.IsControl) >= 0) {
		problems = append(problems, "Remove the control characters.")
	}
	return problems
}

func setupModelCorrection(model string) *onboarding.Correction {
	switch problems := setupModelProblems(model); {
	case len(problems) == 0:
		return nil
	case model == "":
		return setupCorrection("Enter a model identifier", "", problems...)
	default:
		return setupCorrection("Check the model identifier", model, problems...)
	}
}

func (w *setupWizard) prepareAccessChoice(choice *onboarding.AccessChoice) error {
	if !choice.NewAccount {
		_, err := operatorconfig.UpdateAtomic(filepath.Join(w.home, "config.toml"), func(cfg *operatorconfig.Config) error {
			binding := cfg.Runtimes.Bindings[choice.Binding]
			source := cfg.Runtimes.Credentials[binding.Credential]
			if source.Name == "" {
				source.Name = setupDefaultName(*cfg, binding.Credential, setupProviderName(choice.Harness), "connection")
			}
			if binding.Name == "" {
				binding.Name = setupDefaultName(*cfg, choice.Binding, source.Name, "profile")
			}
			cfg.Runtimes.Credentials[binding.Credential] = source
			cfg.Runtimes.Bindings[choice.Binding] = binding
			return cfg.Validate()
		})
		return err
	}
	_, err := operatorconfig.UpdateAtomic(filepath.Join(w.home, "config.toml"), func(cfg *operatorconfig.Config) error {
		if !setupAccessNameAvailable(*cfg, choice.Name, choice.CreatedBinding) {
			return errSetupAccessNameTaken
		}
		binding, exists := cfg.Runtimes.Bindings[choice.Template]
		if !exists || cfg.Runtimes.Credentials[binding.Credential].Kind != setupHarnessKind(choice.Harness) {
			return &setupFailure{cause: "The subscription configuration changed. Select the access method again."}
		}
		source := cfg.Runtimes.Credentials[binding.Credential]
		source.Name = setupDefaultName(*cfg, choice.Name, setupProviderName(choice.Harness), "connection")
		binding.Name = setupDefaultName(*cfg, choice.Name, source.Name, "profile")
		binding.Credential = choice.Name
		if existing, exists := cfg.Runtimes.Bindings[choice.Name]; exists && existing != binding {
			return errSetupAccessNameTaken
		}
		if existing, exists := cfg.Runtimes.Credentials[choice.Name]; exists && existing != source {
			return errSetupAccessNameTaken
		}
		cfg.Runtimes.Credentials[choice.Name], cfg.Runtimes.Bindings[choice.Name] = source, binding
		return cfg.Validate()
	})
	if err != nil {
		return err
	}
	choice.CreatedBinding = choice.Name
	return w.createdConnection(choice.Name)
}

// setupDefaultName keeps the record type at the end of each default name.
func setupDefaultName(cfg operatorconfig.Config, id, base, kind string) string {
	for _, suffix := range []string{" connection", " API key", " subscription"} {
		if strings.HasSuffix(strings.ToLower(base), strings.ToLower(suffix)) {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	used := func(name string) bool {
		if kind == "connection" {
			for key, source := range cfg.Runtimes.Credentials {
				if key != id && strings.EqualFold(source.Name, name) {
					return true
				}
			}
		} else {
			for key, profile := range cfg.Runtimes.Bindings {
				if key != id && strings.EqualFold(profile.Name, name) {
					return true
				}
			}
		}
		return false
	}
	name := base + " " + kind
	for number := 2; used(name); number++ {
		name = fmt.Sprintf("%s %d %s", base, number, kind)
	}
	return name
}
