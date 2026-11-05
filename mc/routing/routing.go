// Package routing parses and validates MC_HOME/routing.md, the authoritative
// role-to-runtime map.
package routing

import (
	"fmt"
	"strings"

	operatorconfig "mc/config"
)

var requiredRoles = []string{
	"strategist", "arbiter", "worker", "verifier", "scribe", "packager", "homie",
}

// Roles returns the routed roles in table order.
func Roles() []string {
	return append([]string(nil), requiredRoles...)
}

// Registry resolves a model binding id to its harness family.
type Registry map[string]string

// ProductionRegistry resolves the enabled bindings of the shipped reference
// catalog. The fake family is absent from production.
func ProductionRegistry() Registry {
	return RegistryFromRuntimes(operatorconfig.Default().Runtimes)
}

// RegistryFromRuntimes resolves only enabled bindings. Config validation
// guarantees every entry exists and names an enabled installed harness.
func RegistryFromRuntimes(runtimes operatorconfig.Runtimes) Registry {
	registry := Registry{}
	for id, spec := range EnabledBindings(runtimes) {
		registry[id] = spec.Harness
	}
	return registry
}

// Route is one validated role row.
type Route struct {
	Role    string
	Harness string
	Binding string
}

// HistoricalBinding is the immutable harness/binding locator stored on Runs.
func (r Route) HistoricalBinding() string { return r.Harness + "/" + r.Binding }

// Table is a complete validated routing snapshot.
type Table struct {
	byRole map[string]Route
}

// Rows returns every route in requiredRoles order (stable; Parse guarantees
// all seven exist, homie included). The read surface for `mc routing show`.
func (t Table) Rows() []Route {
	out := make([]Route, 0, len(requiredRoles))
	for _, role := range requiredRoles {
		if r, ok := t.byRole[role]; ok {
			out = append(out, r)
		}
	}
	return out
}

// Resolve returns the route for a role. Parse guarantees every required role exists.
func (t Table) Resolve(role string) (Route, error) {
	r, ok := t.byRole[role]
	if !ok {
		return Route{}, fmt.Errorf("routing.md has no route for role %q", role)
	}
	return r, nil
}

// WithRoute returns a copy of the table with one role's route replaced. The
// binding must resolve in the registry; the row's harness is the binding's.
func (t Table) WithRoute(role, binding string, registry Registry) (Table, error) {
	if !isRequiredRole(role) {
		return Table{}, fmt.Errorf("unknown role %q", role)
	}
	harness, ok := registry[binding]
	if !ok {
		return Table{}, fmt.Errorf("binding %q is not enabled", binding)
	}
	next := Table{byRole: make(map[string]Route, len(t.byRole))}
	for key, route := range t.byRole {
		next.byRole[key] = route
	}
	next.byRole[role] = Route{Role: role, Harness: harness, Binding: binding}
	return next, nil
}

// UniformTable maps every role to one binding.
func UniformTable(binding string, registry Registry) (Table, error) {
	table := Table{byRole: map[string]Route{}}
	for _, role := range requiredRoles {
		next, err := table.WithRoute(role, binding, registry)
		if err != nil {
			return Table{}, err
		}
		table = next
	}
	return table, nil
}

// Markdown renders the table in the routing.md form that Parse accepts.
func (t Table) Markdown() string {
	var out strings.Builder
	out.WriteString("# Culler routing\n\n")
	out.WriteString("| role | harness | binding |\n")
	out.WriteString("| --- | --- | --- |\n")
	for _, route := range t.Rows() {
		fmt.Fprintf(&out, "| %s | %s | %s |\n", route.Role, route.Harness, route.Binding)
	}
	return out.String()
}

// Parse accepts exactly one Markdown pipe table with columns
// role|harness|binding. Blank lines and # heading/comment lines are allowed;
// prose, extra tables/columns, duplicate/missing/unknown roles, unresolved
// bindings, and harness mismatches fail closed. A table can map every role
// to one binding.
func Parse(data []byte, registry Registry) (Table, error) {
	table := Table{byRole: map[string]Route{}}
	state := 0 // 0 header, 1 separator, 2 data
	for lineNo, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "|") || !strings.HasSuffix(line, "|") {
			return Table{}, fmt.Errorf("routing.md line %d: only the routing table, blank lines, and # comments are allowed", lineNo+1)
		}
		parts := strings.Split(strings.Trim(line, "|"), "|")
		if len(parts) != 3 {
			return Table{}, fmt.Errorf("routing.md line %d: want exactly role|harness|binding", lineNo+1)
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		switch state {
		case 0:
			if parts[0] != "role" || parts[1] != "harness" || parts[2] != "binding" {
				return Table{}, fmt.Errorf("routing.md line %d: first row must be | role | harness | binding |", lineNo+1)
			}
			state = 1
			continue
		case 1:
			for _, part := range parts {
				if strings.Trim(part, "- ") != "" || len(strings.TrimSpace(part)) < 3 {
					return Table{}, fmt.Errorf("routing.md line %d: malformed Markdown separator", lineNo+1)
				}
			}
			state = 2
			continue
		}

		role, harness, binding := parts[0], parts[1], parts[2]
		if !isRequiredRole(role) {
			return Table{}, fmt.Errorf("routing.md line %d: unknown role %q", lineNo+1, role)
		}
		if _, exists := table.byRole[role]; exists {
			return Table{}, fmt.Errorf("routing.md line %d: duplicate role %q", lineNo+1, role)
		}
		resolvedHarness, ok := registry[binding]
		if !ok {
			return Table{}, fmt.Errorf("routing.md line %d: unresolved binding %q", lineNo+1, binding)
		}
		if harness != resolvedHarness {
			return Table{}, fmt.Errorf("routing.md line %d: binding %q resolves to harness %q, not %q", lineNo+1, binding, resolvedHarness, harness)
		}
		table.byRole[role] = Route{Role: role, Harness: harness, Binding: binding}
	}
	if state != 2 {
		return Table{}, fmt.Errorf("routing.md: missing role|harness|binding table")
	}
	if _, ok := table.byRole["scribe"]; !ok {
		if route, exists := table.byRole["worker"]; exists {
			route.Role = "scribe"
			table.byRole["scribe"] = route
		}
	}
	for _, role := range requiredRoles {
		if _, ok := table.byRole[role]; !ok {
			return Table{}, fmt.Errorf("routing.md: missing role %q", role)
		}
	}
	return table, nil
}

func isRequiredRole(got string) bool {
	for _, role := range requiredRoles {
		if got == role {
			return true
		}
	}
	return false
}
