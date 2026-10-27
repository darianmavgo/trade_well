package send_email

import "database/sql"

// EnsureSchema creates the send_email table if it does not already exist.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS send_email (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	to_addr TEXT NOT NULL DEFAULT '',
	subject TEXT NOT NULL DEFAULT '',
	job_context TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT '',
	sent_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_send_email_sent_at ON send_email(sent_at DESC);
`)
	return err
}
