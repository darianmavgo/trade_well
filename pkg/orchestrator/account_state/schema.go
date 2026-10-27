package account_state

import "database/sql"

// EnsureSchema creates every table this package owns: a local mirror of
// Schwab account/position/order/transaction state (DDL lives in schwaber's
// pkg/accountstore). One shared function means the CLI and the dashboard can
// never end up with different schemas depending on which one happened to
// initialize the DB file first.
func EnsureSchema(db *sql.DB) error {
	return store.EnsureSchema(db)
}
