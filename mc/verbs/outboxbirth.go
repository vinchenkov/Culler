package verbs

// Conversation fan-out writes one outbox row per (event, destination). The
// session's bindings resolve the destinations at write time. One of those
// destinations can never be drained.
//
// The `cli` binding has no delivery loop. `mc collie send` binds as `cli`, then
// reads the conversation back with `mc collie history --follow`. The poll and
// the ack belong to each native surface's delivery loop, and the native
// surfaces are the resident and the dashboard. The CLI is neither. So a row is
// born with nobody to drain it, and every CLI turn left one pending forever.
// The pending rows grew monotonically and raised the outbox delivery lag on a
// perfectly healthy board.
//
// A row that nothing can drain must not be born pending. For the CLI the
// conversation row IS the delivery: a chat message is a spine row and a reply
// is a spine row. `mc collie history` reads those rows directly, so there is no
// second hop for `delivered_at` to describe. The code still writes the row,
// one per binding; only its truth value changes.
//
// WHAT THIS IS NOT. It is deliberately not "let the reader ack". An ack from
// `mc collie history` looks natural and is a trap: the DASHBOARD serves
// `/api/collie/history` with that same verb (dashboard/src/api.ts), and polls
// it once a second during an active exchange. The dashboard would then
// silently drain the CLI's rows for replies that no terminal ever read.

import (
	"context"
	"database/sql"
)

// outboxSurfaceCLI is the binding that has no delivery loop.
const outboxSurfaceCLI = "cli"

// insertFanoutOutboxRow writes one fan-out row, stamping `delivered_at` in the
// same statement for the surface that has no drainer.
//
// Every conversation fan-out site goes through here so a fifth one cannot be
// added that regresses this: the rule lives in one place, not four.
func insertFanoutOutboxRow(ctx context.Context, q Q, kind, sessionID, surface, channelRef, payload string) (sql.Result, error) {
	return q.ExecContext(ctx, `
		INSERT INTO outbox (kind, session_id, surface, channel_ref, payload, delivered_at)
		VALUES (?, ?, ?, ?, ?, CASE WHEN ? = ? THEN datetime('now') END)`,
		kind, sessionID, surface, channelRef, payload, surface, outboxSurfaceCLI)
}
