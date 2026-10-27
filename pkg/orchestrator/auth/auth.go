// Package auth is trade_orchestrator's audit log for Schwab auth events plus
// GCS token sync (SyncToken). The interactive OAuth flow is schwaber's
// oauthcli, error classification is schwab.IsPermanentAuthError, and token
// refresh/persistence is owned by schwab.Client (see schwaber/pkg/tokensync);
// this package never triggers a refresh or reload itself. Every logged event
// is event type, detail text, and timestamp only — never raw token values.
package auth

import (
	"database/sql"
	"time"
)

// Recorder returns an audit callback (event, detail) that appends to the auth
// table on db; a nil db yields a no-op. Suitable as oauthcli.Options.OnEvent.
func Recorder(db *sql.DB) func(event, detail string) {
	return func(event, detail string) { logEvent(db, event, detail) }
}

func logEvent(db *sql.DB, event, detail string) {
	if db == nil {
		return
	}
	if err := EnsureSchema(db); err != nil {
		return
	}
	_, _ = db.Exec(
		`INSERT INTO auth (event, detail, logged_at) VALUES (?, ?, ?)`,
		event, detail, time.Now().UTC().Format(time.RFC3339),
	)
}
