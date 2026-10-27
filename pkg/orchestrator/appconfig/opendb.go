package appconfig

import (
	"database/sql"

	"github.com/darianmavgo/trade_well/pkg/schwaber/dbutil"
)

// OpenDB opens the Turso database (dbPath is only used under `go test`, see dbutil.Open). It does
// NOT create any table schema — each owning package's EnsureSchema(db) does
// that, called explicitly by the caller, to avoid an import cycle between
// appconfig and the job packages that depend on it.
func OpenDB(dbPath string) (*sql.DB, error) {
	return dbutil.Open(dbPath)
}
