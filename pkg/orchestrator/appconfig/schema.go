package appconfig

import (
	"database/sql"
	"strings"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// EnsureSchema creates the appconfig log table if it does not already exist.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(orchestratorsql.Q("appconfig/ensure_schema"))
	return err
}

// LogUsed records the resolved config values for this invocation so
// the database always shows exactly what config a given run used.
func (c *Config) LogUsed(db *sql.DB) error {
	if db == nil {
		return nil
	}
	if err := EnsureSchema(db); err != nil {
		return err
	}
	// backtest_root and livescan_bin are NOT NULL columns in databases that
	// already exist (including the live Turso one), so they stay in the
	// table and are always written empty: nothing reads BACKTEST_ROOT or shells
	// out to a livescan binary any more.
	_, err := db.Exec(
		orchestratorsql.Q("appconfig/log_used"),
		dbLabel(c.DBPath), "", c.MarketDB, "",
		strings.Join(c.StrategyAllowlist, ","),
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// dbLabel is what the db_path column records: the file under test, "turso" in production.
func dbLabel(path string) string {
	if path == "" {
		return "turso"
	}
	return path
}
