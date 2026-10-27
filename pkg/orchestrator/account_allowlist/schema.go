package account_allowlist

import "database/sql"

const schemaDDL = `
CREATE TABLE IF NOT EXISTS account_allowlist (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_number TEXT,
  enabled BOOLEAN,
  loaded_at TIMESTAMP
);
`

// EnsureSchema creates the account_allowlist audit table if it doesn't exist.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(schemaDDL)
	return err
}
