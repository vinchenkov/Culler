package verbs

import (
	"database/sql"
	"time"
)

// AcceptanceRuntimeSnapshot is the path-free, read-only half of the sleep
// drill. It samples the VM-resident mc clock, every Project's lease row,
// and the settings row in one helper invocation; it never
// manufactures a lease.
func AcceptanceRuntimeSnapshot(db *sql.DB, now time.Time) (any, error) {
	leases, err := LeaseList(db)
	if err != nil {
		return nil, err
	}
	settings, err := SettingsGet(db)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"version":  1,
		"vm_now":   now.UTC().Format(time.RFC3339Nano),
		"leases":   leases.(map[string]any)["leases"],
		"settings": settings,
	}, nil
}
