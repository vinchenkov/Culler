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

const (
	setupAccessNameRule = "Use lowercase letters, digits, periods, underscores, or hyphens."
)

var (
	// errSetupAccessNameTaken reports a name that Culler took for another use after the name prompt.
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
		choice.NewAccount, choice.Step = true, "account-name"
		for number := 2; ; number++ {
			name := fmt.Sprintf("%s-%d", harness, number)
			if setupAccessNameAvailable(cfg, name, "") {
				choice.Name = name
				break
			}
		}
	}
	return choice, nil
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
	accountIntroduced := false
	var correction *onboarding.Correction
	for w.session.Access != nil {
		choice := w.session.Access
		if err := w.save(); err != nil {
			return err
		}
		var err error
		if choice.Mode == "api_key" && choice.Step != "check" {
			err = w.apiKeyStep(choice)
		} else {
			switch choice.Step {
			case "account-name":
				if !accountIntroduced {
					w.ui.Panel("Add an account", []string{"Choose a name to identify this account in Culler."})
					accountIntroduced = true
				}
				var name string
				name, err = w.askAccessName(choice, onboarding.Prompt{
					Label: "Name this account", Hints: []string{setupAccessNameRule}, Value: choice.Name, Correction: correction,
				})
				if err == nil {
					choice.Name, choice.Binding, choice.Step = name, name, "check"
				}
			case "check":
				connection := setupConnection{Binding: choice.Binding, Mode: choice.Mode}
				if choice.Mode == "login" {
					connection.Prepare = func() error { return w.prepareAccessChoice(choice) }
				}
				err = w.checkConnection(connection)
				if errors.Is(err, errSetupAccessNameTaken) {
					correction = setupAccessNameCorrection(choice, choice.Name)
					choice.Step = "account-name"
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
		}
		if choice.Step != "account-name" {
			correction = nil
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
		case "model":
			choice.Step = "key"
		default:
			choice.Step = ""
		}
		return choice.Step != ""
	}
	if choice.Step == "check" && choice.NewAccount {
		choice.Step = "account-name"
	} else {
		choice.Step = ""
	}
	return choice.Step != ""
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

// askAccessName returns a name that no other agent profile or connection uses.
func (w *setupWizard) askAccessName(choice *onboarding.AccessChoice, prompt onboarding.Prompt) (string, error) {
	var catalogErr error
	prompt.Check = func(name string) *onboarding.Correction {
		cfg, err := w.catalog()
		if catalogErr = err; err != nil || setupAccessNameAvailable(cfg, name, choice.CreatedBinding) {
			return nil
		}
		return setupAccessNameCorrection(choice, name)
	}
	name, err := w.ask(prompt)
	if err == nil && catalogErr != nil {
		return "", catalogErr
	}
	return name, err
}

func setupAccessNameCorrection(choice *onboarding.AccessChoice, name string) *onboarding.Correction {
	subject, article := "account", "an"
	switch {
	case name == "":
		return setupCorrection("Enter "+article+" "+subject+" name", "", "The "+subject+" name is empty.")
	case !setupAccessName.MatchString(name):
		return setupCorrection("Check the "+subject+" name", name, setupAccessNameProblems(name)...)
	}
	return setupCorrection("Choose another "+subject+" name", name, "Culler already uses this name.")
}

func setupAccessNameProblems(name string) []string {
	problems := []string{}
	if strings.ContainsFunc(name, func(r rune) bool { return r >= 'A' && r <= 'Z' }) {
		problems = append(problems, "Change each uppercase letter to a lowercase letter.")
	}
	if strings.ContainsFunc(name, unicode.IsSpace) {
		problems = append(problems, "Replace each space with a hyphen.")
	}
	if strings.ContainsFunc(name, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !unicode.IsSpace(r) && !strings.ContainsRune("._-", r)
	}) {
		problems = append(problems, setupAccessNameRule)
	}
	if strings.ContainsAny(name[:1], "._-") {
		problems = append(problems, "Start the name with a lowercase letter or a digit.")
	}
	if utf8.RuneCountInString(name) > 64 {
		problems = append(problems, "Use a maximum of 64 characters.")
	}
	return problems
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
		return nil
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
		source.Name, binding.Name, binding.Credential = choice.Name, choice.Name, choice.Name
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
	return w.save()
}
