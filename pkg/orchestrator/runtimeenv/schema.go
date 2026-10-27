package runtimeenv

import (
	"database/sql"
)

const schemaDDL = `
CREATE TABLE IF NOT EXISTS runtimeenv (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  environment TEXT,
  service TEXT,
  version TEXT,
  project TEXT,
  detected_at TIMESTAMP
);
`

// EnsureSchema creates the runtimeenv audit table if it doesn't exist.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(schemaDDL)
	return err
}
