package verbs

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"mc/routing"
)

// RoutingShow renders the role-to-runtime table for the dashboard: one row
// per required role, collie included, with the binding's configured model,
// plus the enabled bindings a role can move to. Routing.md is its own
// operator file with its own fenced reader, so this is a separate verb
// rather than a `mc config show` section.
func RoutingShow() (any, error) {
	home, err := mcHomeDir()
	if err != nil {
		return nil, err
	}
	authority, err := loadRuntimeAuthority(home)
	if err != nil {
		return nil, Domainf("load runtime authority: %v", err)
	}
	table, _, err := readRoutingTable(home, authority)
	if err != nil {
		return nil, err
	}
	return routingTablePayload(authority, table), nil
}

// RoutingSet replaces the binding of one role. The verb accepts only an
// enabled binding and writes the complete table under the same validation as
// onboarding. It returns the table that RoutingShow returns.
func RoutingSet(role, binding, expected string) (any, error) {
	home, err := mcHomeDir()
	if err != nil {
		return nil, err
	}
	if expected == "" {
		return nil, Usagef("The role assignment requires its previous binding.")
	}
	lock, err := os.OpenFile(filepath.Join(home, "routing.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	authority, err := loadRuntimeAuthority(home)
	if err != nil {
		return nil, Domainf("load runtime authority: %v", err)
	}
	table, path, err := readRoutingTable(home, authority)
	if err != nil {
		return nil, err
	}
	next, err := table.WithRoute(role, binding, authority.registry)
	if err != nil {
		return nil, Domainf("routing set: %v", err)
	}
	current, err := table.Resolve(role)
	if err != nil {
		return nil, Domainf("routing set: %v", err)
	}
	if current.Binding != expected && current.Binding != binding {
		return nil, Domainf("The role assignment changed. Read the current assignment before another change.")
	}
	candidate := []byte(next.Markdown())
	if _, err := routing.Parse(candidate, authority.registry); err != nil {
		return nil, Domainf("routing set produced an invalid table: %v", err)
	}
	if err := replaceRoutingTable(home, path, candidate); err != nil {
		return nil, err
	}
	written, _, err := readRoutingTable(home, authority)
	if err != nil {
		return nil, err
	}
	return routingTablePayload(authority, written), nil
}

func readRoutingTable(home string, authority runtimeAuthority) (routing.Table, string, error) {
	path := filepath.Join(home, "routing.md")
	data, err := readRoutingPath(path)
	if err != nil {
		return routing.Table{}, "", err
	}
	table, err := routing.Parse(data, authority.registry)
	if err != nil {
		return routing.Table{}, "", Domainf("invalid routing.md at %q: %v", path, err)
	}
	return table, path, nil
}

func routingTablePayload(authority runtimeAuthority, table routing.Table) map[string]any {
	name := func(id string) string {
		if profile, ok := authority.config.Runtimes.Bindings[id]; ok && profile.Name != "" {
			return profile.Name
		}
		return id
	}

	model := func(binding string) string {
		if spec, ok := authority.configuredBindings[binding]; ok {
			return spec.Model
		}
		return ""
	}
	routes := make([]map[string]any, 0, 7)
	for _, route := range table.Rows() {
		routes = append(routes, map[string]any{
			"role": route.Role, "harness": route.Harness,
			"binding": route.Binding, "name": name(route.Binding), "model": model(route.Binding),
		})
	}
	ids := make([]string, 0, len(authority.registry))
	for id := range authority.registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	bindings := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		bindings = append(bindings, map[string]any{
			"id": id, "name": name(id), "harness": authority.registry[id], "model": model(id),
		})
	}
	return map[string]any{"routes": routes, "bindings": bindings}
}

// replaceRoutingTable publishes a validated table over the existing
// routing.md with a temp file and a rename, so a crash cannot publish a
// partial table.
func replaceRoutingTable(home, path string, candidate []byte) error {
	tmp, err := os.CreateTemp(home, ".routing.md.tmp-*")
	if err != nil {
		return Domainf("create routing.md temp file: %v", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return Domainf("protect routing.md temp file: %v", err)
	}
	if _, err := tmp.Write(candidate); err != nil {
		tmp.Close()
		return Domainf("write routing.md temp file: %v", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return Domainf("sync routing.md temp file: %v", err)
	}
	if err := tmp.Close(); err != nil {
		return Domainf("close routing.md temp file: %v", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return Domainf("replace routing.md: %v", err)
	}
	return nil
}
