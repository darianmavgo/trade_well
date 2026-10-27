package dashboard

import (
	"database/sql"
	"time"
)

const schemaDDL = `
CREATE TABLE IF NOT EXISTS dashboard (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  port TEXT,
  started_at TIMESTAMP
);
`

// EnsureSchema creates the dashboard audit table if it doesn't exist.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(schemaDDL)
	return err
}

func logServerStart(db *sql.DB, port int) error {
	if db == nil {
		return nil
	}
	if err := EnsureSchema(db); err != nil {
		return err
	}
	_, err := db.Exec(`INSERT INTO dashboard (port, started_at) VALUES (?, ?)`, port, time.Now().UTC())
	return err
}
