package verbs

import (
	"context"
	"database/sql"
	"strings"
	"unicode"
	"unicode/utf8"
)

// CollieTitle lets the host rename a conversation. An agent can set its own title once.
func CollieTitle(db *sql.DB, id *RunIdentity, session, title string) (any, error) {
	title = strings.TrimSpace(title)
	if !utf8.ValidString(title) || utf8.RuneCountInString(title) < 1 || utf8.RuneCountInString(title) > 100 {
		return nil, Usagef("The conversation name must contain 1 to 100 characters.")
	}
	for _, r := range title {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return nil, Usagef("The conversation name must contain one line of text.")
		}
	}
	if id != nil && (id.Tier != "collie" || id.RunID != session) {
		return nil, Domainf("A Collie agent can name only its own conversation.")
	}
	changed := false
	err := inTx(db, func(ctx context.Context, q Q) error {
		if err := requireOperatorVerbTx(ctx, q, id, "collie.title"); err != nil {
			return err
		}
		var current sql.NullString
		if err := q.QueryRowContext(ctx, `SELECT title FROM collie_sessions WHERE id=? AND deleted_at IS NULL`, session).Scan(&current); err != nil {
			if err == sql.ErrNoRows {
				return Domainf("The conversation is unavailable.")
			}
			return err
		}
		if current.Valid && (id != nil || current.String == title) {
			title = current.String
			return nil
		}
		if id != nil {
			var messages int
			if err := q.QueryRowContext(ctx, `SELECT count(*) FROM conversation_messages WHERE session_id=? AND direction='inbound'`, session).Scan(&messages); err != nil {
				return err
			}
			if messages == 0 {
				return Domainf("The conversation needs a message before Collie can name it.")
			}
		}
		if _, err := q.ExecContext(ctx, `UPDATE collie_sessions SET title=? WHERE id=?`, title, session); err != nil {
			return err
		}
		actor := "operator"
		if id != nil {
			actor = "collie"
		}
		if _, err := q.ExecContext(ctx, `INSERT INTO activity (actor,kind,subject,detail) VALUES (?, 'collie.title', ?, ?)`, actor, session, title); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"session_id": session, "title": title, "changed": changed}, nil
}
