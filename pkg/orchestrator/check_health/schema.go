package check_health

import (
	"database/sql"
	"fmt"
	"time"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"
)

// EnsureSchema creates the check_health, check_health_finding, and
// check_health_audit tables if they do not already exist, and seeds the
// built-in data-driven checks.
func EnsureSchema(db *sql.DB) error {
	schema := orchestratorsql.Q("check_health/ensure_schema")
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	return seedDefaultHealthChecks(db)
}

// defaultHealthChecks are the built-in, data-driven checks run against the
// Turso account_state mirror by RunSQLHealthChecks. Each query is parameterized by
// account_number as its sole placeholder and must return exactly two text
// columns: subject (e.g. a symbol), then detail (a human-readable reason) --
// one row per violation found. Table names reflect this repo's pkg/
// restructure: active_trades -> manage_exit, staged_orders -> stage_order,
// accounts -> account_state_account, positions -> account_state_position.
var defaultHealthChecks = []Def{
	{
		Name:        "stale_time_expired_trade",
		Category:    "job_lifecycle",
		Severity:    "CRITICAL",
		Description: "A manage_exit row is still OPEN after its exit date (max_trading_days trading days after the entry fill date) -- manage-exits should have closed it.",
		Query:       orchestratorsql.Q("check_health/check_stale_time_expired_trade"),
	},
	{
		Name:        "exit_date_not_from_fill",
		Category:    "job_lifecycle",
		Severity:    "WARNING",
		Description: "An OPEN position's exit date is still an estimate more than a day after entry: it should have been corrected from the entry order's fill date (manage-exits does this from the broker's order list).",
		Query:       orchestratorsql.Q("check_health/check_exit_date_not_from_fill"),
	},
	{
		Name:        "stuck_pending_fill",
		Category:    "job_lifecycle",
		Severity:    "WARNING",
		Description: "An entry order the broker accepted was never confirmed filled or cancelled after 3 days.",
		Query:       orchestratorsql.Q("check_health/check_stuck_pending_fill"),
	},
	{
		Name:        "duplicate_open_trade",
		Category:    "data_integrity",
		Severity:    "WARNING",
		Description: "More than one OPEN manage_exit row exists for the same symbol in the same account.",
		Query:       orchestratorsql.Q("check_health/check_duplicate_open_trade"),
	},
	{
		Name:        "stuck_pending_staged_order",
		Category:    "job_lifecycle",
		Severity:    "WARNING",
		Description: "A stage_order row has been PENDING for more than 2 days -- submit-orders has not picked it up.",
		Query:       orchestratorsql.Q("check_health/check_stuck_pending_staged_order"),
	},
	{
		Name:        "recent_rejected_order",
		Category:    "job_lifecycle",
		Severity:    "WARNING",
		Description: "A staged order was rejected by submit-orders in the last 3 days.",
		Query:       orchestratorsql.Q("check_health/check_recent_rejected_order"),
	},
	{
		Name:        "stale_account_snapshot",
		Category:    "data_freshness",
		Severity:    "WARNING",
		Description: "The local account_state mirror for this account has not been refreshed by account_state.Sync in over 20 hours.",
		Query:       orchestratorsql.Q("check_health/check_stale_account_snapshot"),
	},
	{
		Name:        "negative_available_funds",
		Category:    "risk",
		Severity:    "CRITICAL",
		Description: "The account's available funds for trading are negative.",
		Query:       orchestratorsql.Q("check_health/check_negative_available_funds"),
	},
	{
		Name:        "position_concentration",
		Category:    "risk",
		Severity:    "WARNING",
		Description: "A single held position is more than 50% of the account's liquidation value.",
		Query:       orchestratorsql.Q("check_health/check_position_concentration"),
	},
	{
		Name:        "orphan_active_trade",
		Category:    "data_integrity",
		Severity:    "WARNING",
		Description: "manage_exit marks a symbol OPEN but the local broker mirror shows no held quantity for it.",
		Query:       orchestratorsql.Q("check_health/check_orphan_active_trade"),
	},
}

// seedDefaultHealthChecks installs/updates the built-in checks by name. The
// UPDATE clause deliberately omits `enabled`: an operator's on/off choice
// for a given check must survive a code upgrade that only changes its query
// or description.
func seedDefaultHealthChecks(db *sql.DB) error {
	now := time.Now().UTC()
	for _, c := range defaultHealthChecks {
		_, err := db.Exec(orchestratorsql.Q("check_health/seed_default_health_checks"),
			c.Name, c.Category, c.Severity, c.Query, c.Description, now)
		if err != nil {
			return fmt.Errorf("seed health check %q: %w", c.Name, err)
		}
	}
	return nil
}
