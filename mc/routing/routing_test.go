package routing

import (
	"strings"
	"testing"
)

const canonical = `# Culler routing
| role | harness | binding |
| --- | --- | --- |
| strategist | claude-sdk | claude |
| arbiter | codex | chatgpt |
| worker | claude-sdk | endpoint |
| verifier | codex | chatgpt |
| packager | claude-sdk | endpoint |
| homie | claude-sdk | claude |
`

func referenceRegistry(t *testing.T) Registry {
	t.Helper()
	return RegistryFromRuntimes(endpointConfig(t).Runtimes)
}

func TestParseCanonicalRouting(t *testing.T) {
	table, err := Parse([]byte(canonical), referenceRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	r, err := table.Resolve("worker")
	if err != nil {
		t.Fatal(err)
	}
	if r.Harness != "claude-sdk" || r.Binding != "endpoint" || r.HistoricalBinding() != "claude-sdk/endpoint" {
		t.Fatalf("worker route = %+v", r)
	}
}

func TestParseAcceptsOneBindingForEveryRole(t *testing.T) {
	registry := referenceRegistry(t)
	for _, binding := range []string{"claude", "chatgpt", "second-endpoint"} {
		table, err := UniformTable(binding, registry)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := Parse([]byte(table.Markdown()), registry)
		if err != nil {
			t.Fatalf("uniform %s table refused: %v", binding, err)
		}
		for _, route := range parsed.Rows() {
			if route.Binding != binding || route.Harness != registry[binding] {
				t.Fatalf("uniform %s row = %+v", binding, route)
			}
		}
	}
	if _, err := UniformTable("disabled", registry); err == nil {
		t.Fatal("a uniform table on a disabled binding was accepted")
	}
}

func TestWithRouteReplacesOneRoleAndRoundTrips(t *testing.T) {
	registry := referenceRegistry(t)
	table, err := Parse([]byte(canonical), registry)
	if err != nil {
		t.Fatal(err)
	}
	next, err := table.WithRoute("worker", "second-endpoint", registry)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := table.Resolve("worker"); got.Binding != "endpoint" {
		t.Fatalf("WithRoute mutated the source table: %+v", got)
	}
	parsed, err := Parse([]byte(next.Markdown()), registry)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := parsed.Resolve("worker"); got.Binding != "second-endpoint" || got.Harness != "claude-sdk" {
		t.Fatalf("worker route after set = %+v", got)
	}
	if got, _ := parsed.Resolve("arbiter"); got.Binding != "chatgpt" {
		t.Fatalf("arbiter route changed: %+v", got)
	}
	if _, err := table.WithRoute("critic", "second-endpoint", registry); err == nil || !strings.Contains(err.Error(), "unknown role") {
		t.Fatalf("unknown role error = %v", err)
	}
	if _, err := table.WithRoute("worker", "studio", registry); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("disabled binding error = %v", err)
	}
}

func TestParseRoutingRejectsInvalidConfigurations(t *testing.T) {
	tests := []struct {
		name string
		body string
		msg  string
	}{
		{"prose", "not a table\n" + canonical, "only the routing table"},
		{"extra_column", strings.Replace(canonical, "| role | harness | binding |", "| role | harness | binding | model |", 1), "exactly"},
		{"missing_role", strings.Replace(canonical, "| packager | claude-sdk | endpoint |\n", "", 1), "missing role"},
		{"duplicate_role", canonical + "| worker | claude-sdk | endpoint |\n", "duplicate role"},
		{"unknown_role", strings.Replace(canonical, "| packager |", "| critic |", 1), "unknown role"},
		{"unknown_binding", strings.Replace(canonical, "| worker | claude-sdk | endpoint |", "| worker | claude-sdk | missing |", 1), "unresolved binding"},
		{"harness_mismatch", strings.Replace(canonical, "| worker | claude-sdk | endpoint |", "| worker | codex | endpoint |", 1), "resolves to harness"},
	}
	registry := referenceRegistry(t)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.body), registry)
			if err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("error = %v, want containing %q", err, tc.msg)
			}
		})
	}
}

func TestProductionRegistryRejectsFake(t *testing.T) {
	fake := strings.NewReplacer(
		"claude-sdk", "fake",
		"codex", "fake",
		"claude", "fake",
		"chatgpt", "fake",
		"endpoint", "fake",
	).Replace(canonical)
	if _, err := Parse([]byte(fake), referenceRegistry(t)); err == nil || !strings.Contains(err.Error(), "unresolved binding") {
		t.Fatalf("production fake error = %v", err)
	}
}

func TestRowsReturnsEveryRoleInStableOrder(t *testing.T) {
	table, err := Parse([]byte(canonical), referenceRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	rows := table.Rows()
	if len(rows) != len(requiredRoles) {
		t.Fatalf("Rows() returned %d routes, want %d", len(rows), len(requiredRoles))
	}
	for i, role := range requiredRoles {
		if rows[i].Role != role {
			t.Fatalf("Rows()[%d].Role = %q, want %q", i, rows[i].Role, role)
		}
	}
	if last := rows[len(rows)-1]; last.Role != "homie" || last.Harness != "claude-sdk" {
		t.Fatalf("homie row = %+v", last)
	}
}
