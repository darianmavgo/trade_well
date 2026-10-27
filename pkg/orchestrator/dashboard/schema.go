package dashboard

import (
	"database/sql"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

var schemaDDL = orchestratorsql.Q("dashboard/schema_ddl")

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
	_, err := db.Exec(orchestratorsql.Q("dashboard/log_server_start"), port, time.Now().UTC())
	return err
}
