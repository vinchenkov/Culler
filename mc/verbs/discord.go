package verbs

import (
	"context"
	"database/sql"
	"regexp"
)

type DiscordProjection struct {
	Connection string `json:"connection"`
	Enabled    bool   `json:"enabled"`
	Channel    string `json:"channel"`
	Blocked    bool   `json:"blocked"`
	Health     bool   `json:"health"`
	Retire     bool   `json:"retire"`
}

func DiscordAvailable(db *sql.DB, configured bool) (any, error) {
	available := false
	err := inTx(db, func(ctx context.Context, q Q) error {
		_, err := q.ExecContext(ctx, `UPDATE settings SET discord_available=1 WHERE id=1 AND
            (? OR EXISTS (SELECT 1 FROM projects p JOIN environment_operations e ON e.project_id=p.id
                WHERE p.setup_status='ready' AND e.kind='accept' AND e.status='succeeded'
                AND json_extract(e.metadata,'$.initial_setup')=1
                AND coalesce(json_extract(e.metadata,'$.candidate'),0)=0))`, configured)
		if err != nil {
			return err
		}
		return q.QueryRowContext(ctx, `SELECT discord_available FROM settings WHERE id=1`).Scan(&available)
	})
	return map[string]any{"available": available}, err
}

func DiscordApply(db *sql.DB, request DiscordProjection) (any, error) {
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(request.Connection) ||
		(request.Enabled && !regexp.MustCompile(`^[0-9]{1,20}$`).MatchString(request.Channel)) ||
		(!request.Enabled && (request.Channel != "" || request.Blocked || request.Health)) {
		return nil, Usagef("The Discord destination is invalid.")
	}
	err := inTx(db, func(ctx context.Context, q Q) error {
		var previous string
		if err := q.QueryRowContext(ctx, `SELECT discord_connection FROM settings WHERE id=1`).Scan(&previous); err != nil {
			return err
		}
		if request.Retire || (previous != "" && previous != request.Connection) {
			if _, err := q.ExecContext(ctx, `UPDATE homie_discord_threads SET retired_at=datetime('now') WHERE retired_at IS NULL`); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `UPDATE homie_bindings SET active=0 WHERE surface='discord' AND active=1`); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `UPDATE outbox SET retired_at=datetime('now') WHERE surface='discord' AND delivered_at IS NULL AND retired_at IS NULL`); err != nil {
				return err
			}
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM alert_routes WHERE surface='discord'`); err != nil {
			return err
		}
		for class, enabled := range map[string]bool{"blocked": request.Blocked, "health": request.Health} {
			if !enabled {
				continue
			}
			if _, err := q.ExecContext(ctx, `INSERT INTO alert_routes(class,ordinal,surface,channel_ref)
                SELECT ?,coalesce(max(ordinal),-1)+1,'discord',? FROM alert_routes WHERE class=?`, class, request.Channel, class); err != nil {
				return err
			}
		}
		_, err := q.ExecContext(ctx, `UPDATE settings SET discord_connection=?,discord_available=max(discord_available,?) WHERE id=1`, request.Connection, request.Enabled)
		return err
	})
	return map[string]any{"applied": err == nil}, err
}

func discordID(value string) bool { return regexp.MustCompile(`^[0-9]{1,20}$`).MatchString(value) }
