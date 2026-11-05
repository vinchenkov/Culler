package verbs

import (
	"context"
	"database/sql"
	"time"

	"mc/domain"
)

type ProviderLaunchIdentity struct {
	RunID    string `json:"run_id"`
	LaunchID string `json:"launch_id"`
}

type ProviderLaunchLifetime struct {
	ProviderLaunchIdentity
	Allowed   bool   `json:"allowed"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

// ProviderAccessLifetimes derives temporary authority from current control state.
func ProviderAccessLifetimes(db *sql.DB, id *RunIdentity, launches []ProviderLaunchIdentity) (any, error) {
	if err := RequireHostScope(id, "provider access lifetime"); err != nil {
		return nil, err
	}
	if len(launches) > 100 {
		return nil, Usagef("The provider access request exceeds its limit.")
	}
	clock, err := domain.ReadVMClock()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	results := make([]ProviderLaunchLifetime, 0, len(launches))
	for _, launch := range launches {
		if launch.RunID == "" || len(launch.RunID) > 128 || launch.LaunchID == "" || len(launch.LaunchID) > 128 {
			return nil, Usagef("The provider launch identity is invalid.")
		}
		row := ProviderLaunchLifetime{ProviderLaunchIdentity: launch}
		expiry, err := providerLaunchExpiry(context.Background(), db, launch, now, clock)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil && expiry.After(now) {
			row.Allowed = true
			row.ExpiresAt = expiry.Format(time.RFC3339Nano)
		}
		results = append(results, row)
	}
	return map[string]any{"launches": results}, nil
}

func providerLaunchExpiry(ctx context.Context, q Q, launch ProviderLaunchIdentity, now time.Time, clock domain.VMClock) (time.Time, error) {
	var current, state, last string
	var idle int64
	err := q.QueryRowContext(ctx, `SELECT COALESCE(current_launch_id,''),status,last_activity_at,
 (SELECT homie_idle_timeout_s FROM settings WHERE id=1) FROM homie_sessions WHERE id=?`, launch.RunID).Scan(&current, &state, &last, &idle)
	if err == nil {
		if state != "active" || current != launch.LaunchID {
			return time.Time{}, nil
		}
		activity, err := parseSpineTime(last)
		if err != nil {
			return time.Time{}, err
		}
		return activity.Add(time.Duration(idle) * time.Second), nil
	}
	if err != sql.ErrNoRows {
		return time.Time{}, err
	}
	if launch.RunID != launch.LaunchID {
		return time.Time{}, nil
	}
	var boot string
	var deadline int64
	err = q.QueryRowContext(ctx, `SELECT l.boot_id,l.deadline_uptime_ms FROM leases l JOIN runs r ON r.id=l.run_id
 WHERE l.run_id=? AND r.ended_at IS NULL
 UNION ALL SELECT w.boot_id,w.deadline_uptime_ms FROM wiki_updates w JOIN runs r ON r.id=w.run_id
 WHERE w.run_id=? AND w.ended_at IS NULL AND r.ended_at IS NULL`, launch.RunID, launch.RunID).Scan(&boot, &deadline)
	if err == nil {
		if boot != clock.BootID || deadline <= clock.Uptime.Milliseconds() {
			return time.Time{}, nil
		}
		return now.Add(time.Duration(deadline-clock.Uptime.Milliseconds()) * time.Millisecond), nil
	}
	if err != sql.ErrNoRows {
		return time.Time{}, err
	}
	var created string
	// The resident issues access before the container sends its first heartbeat.
	err = q.QueryRowContext(ctx, `SELECT o.created_at FROM environment_operations o
 JOIN project_environments e ON e.project_id=o.project_id AND e.active_operation_id=o.id
 JOIN projects p ON p.id=o.project_id
 WHERE o.id=? AND o.status IN ('queued','running') AND o.kind IN ('setup','validate')
 AND o.ended_at IS NULL AND p.status<>'archived'`, launch.RunID).Scan(&created)
	if err != nil {
		return time.Time{}, err
	}
	started, err := parseSpineTime(created)
	return started.Add(30 * time.Minute), err
}
