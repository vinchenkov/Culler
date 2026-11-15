// Package operatorcmd defines the supported operator command contracts.
package operatorcmd

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const FormatVersion = 1

type Field struct {
	Name             string   `json:"name"`
	Type             string   `json:"type"`
	Description      string   `json:"description"`
	Required         bool     `json:"required"`
	RequirementCheck string   `json:"requirement_check,omitempty"`
	Default          any      `json:"default,omitempty"`
	Values           []string `json:"values,omitempty"`
	Minimum          *int64   `json:"minimum,omitempty"`
	MinimumMessage   string   `json:"minimum_refusal,omitempty"`
	Maximum          *int64   `json:"maximum,omitempty"`
	MaxBytes         int      `json:"max_bytes,omitempty"`
	RequiredWhen     string   `json:"required_when,omitempty"`
	Fields           []Field  `json:"fields,omitempty"`
}

type Example struct {
	Arguments []string `json:"arguments"`
	Request   any      `json:"request,omitempty"`
	Result    any      `json:"result"`
}

type Definition struct {
	Path          []string  `json:"path"`
	Purpose       string    `json:"purpose"`
	Permission    string    `json:"permission,omitempty"`
	Mutates       bool      `json:"mutates"`
	Host          bool      `json:"host_execution"`
	Scope         string    `json:"scope"`
	Arguments     []Field   `json:"arguments"`
	Flags         []Field   `json:"flags"`
	Request       []Field   `json:"request_fields,omitempty"`
	RequestSource string    `json:"request_source,omitempty"`
	RequestLimit  int64     `json:"request_limit_bytes,omitempty"`
	Result        []Field   `json:"result_fields"`
	Requirements  []string  `json:"requirements"`
	Refusals      []string  `json:"refusals"`
	Retry         string    `json:"retry"`
	Asynchronous  bool      `json:"asynchronous"`
	StatusRead    string    `json:"status_read,omitempty"`
	Examples      []Example `json:"examples"`
	OneOf         []string  `json:"one_of_flags,omitempty"`
}

func (d Definition) Name() string { return strings.Join(d.Path, " ") }

func Definitions() []Definition { return definitions() }

func Lookup(args []string) (Definition, bool) {
	var found Definition
	for _, d := range definitions() {
		if len(args) >= len(d.Path) && len(d.Path) > len(found.Path) && slices.Equal(args[:len(d.Path)], d.Path) {
			found = d
		}
	}
	if len(found.Path) == 0 {
		return retainedDefinition(args)
	}
	return found, true
}

func Permissions() []string {
	var result []string
	for _, d := range definitions() {
		if d.Permission != "" && !slices.Contains(result, d.Permission) {
			result = append(result, d.Permission)
		}
	}
	slices.Sort(result)
	return result
}

// ValidateArguments uses the same fields that help returns.
func (d Definition) ValidateArguments(args []string) error {
	args = args[len(d.Path):]
	position := 0
	seen := map[string]string{}
	for len(args) > 0 {
		arg := args[0]
		args = args[1:]
		if !strings.HasPrefix(arg, "-") {
			if len(seen) > 0 || position >= len(d.Arguments) {
				return fmt.Errorf("mc %s has an unexpected argument %q", d.Name(), arg)
			}
			if err := validateValue(d.Arguments[position], arg); err != nil {
				return err
			}
			position++
			continue
		}
		if !strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "---") {
			return fmt.Errorf("mc %s requires flags with two leading hyphens", d.Name())
		}
		name, value, assigned := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if _, exists := seen[name]; exists {
			return fmt.Errorf("mc %s accepts --%s once", d.Name(), name)
		}
		var field *Field
		for i := range d.Flags {
			if d.Flags[i].Name == name {
				field = &d.Flags[i]
				break
			}
		}
		if field == nil {
			return fmt.Errorf("mc %s has no flag --%s", d.Name(), name)
		}
		if !assigned {
			if field.Type == "boolean" {
				value = "true"
			} else {
				if len(args) == 0 {
					return fmt.Errorf("--%s requires a value", name)
				}
				value, args = args[0], args[1:]
			}
		}
		if err := validateValue(*field, value); err != nil {
			return err
		}
		seen[name] = value
	}
	for _, field := range d.Arguments[position:] {
		if field.Required {
			return fmt.Errorf("mc %s requires <%s>", d.Name(), field.Name)
		}
	}
	for _, field := range d.Flags {
		if _, exists := seen[field.Name]; field.Required && field.RequirementCheck != "domain" && !exists {
			return fmt.Errorf("mc %s requires --%s", d.Name(), field.Name)
		}
	}
	if len(d.OneOf) != 0 {
		count := 0
		for _, name := range d.OneOf {
			if value, ok := seen[name]; ok && value == "true" {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("mc %s requires exactly one of --%s", d.Name(), strings.Join(d.OneOf, ", --"))
		}
	}
	return nil
}

func validateValue(field Field, value string) error {
	if field.Required && strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s requires a value", field.Name)
	}
	if len(field.Values) > 0 && !slices.Contains(field.Values, value) {
		return fmt.Errorf("%s accepts %s", field.Name, strings.Join(field.Values, ", "))
	}
	switch field.Type {
	case "integer":
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return fmt.Errorf("%s requires an integer", field.Name)
		}
		if field.Minimum != nil && n < *field.Minimum {
			if field.MinimumMessage != "" {
				return fmt.Errorf("%s", field.MinimumMessage)
			}
			return fmt.Errorf("%s requires at least %d", field.Name, *field.Minimum)
		}
		if field.Maximum != nil && n > *field.Maximum {
			return fmt.Errorf("%s permits at most %d", field.Name, *field.Maximum)
		}
	case "boolean":
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("%s requires a boolean", field.Name)
		}
	}
	return nil
}

func NearestHelp(args []string) string {
	if len(args) > 0 {
		for _, d := range definitions() {
			if d.Path[0] == args[0] {
				return "mc " + args[0] + " --help"
			}
		}
	}
	return "mc --help"
}
