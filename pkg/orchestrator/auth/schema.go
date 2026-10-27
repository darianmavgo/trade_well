package auth

import "database/sql"

// EnsureSchema creates the auth log table if it does not already exist. No
// raw token values are ever stored here — only event types, detail text, and
// timestamps (per the user's "prints tokens, refresh timestamps" — meaning
// the fact and timing of an issue/refresh, not the secret itself).
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS auth (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	event TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	logged_at DATETIME NOT NULL
);
`)
	return err
}
