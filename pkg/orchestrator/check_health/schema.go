package check_health

import (
	"database/sql"
	"fmt"
	"time"
)

// EnsureSchema creates the check_health, check_health_finding, and
// check_health_audit tables if they do not already exist, and seeds the
// built-in data-driven checks.
func EnsureSchema(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS check_health_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		account_number TEXT NOT NULL,
		timestamp DATETIME NOT NULL,
		total_positions INTEGER NOT NULL,
		protected_positions INTEGER NOT NULL,
		unprotected_positions INTEGER NOT NULL,
		orphan_orders INTEGER NOT NULL,
		status TEXT NOT NULL,
		notes TEXT,
		critical_findings INTEGER NOT NULL DEFAULT 0,
		warning_findings INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS check_health (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		category TEXT NOT NULL,
		severity TEXT NOT NULL DEFAULT 'WARNING',
		enabled INTEGER NOT NULL DEFAULT 1,
		query TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS check_health_finding (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_at DATETIME NOT NULL,
		account_number TEXT NOT NULL,
		check_name TEXT NOT NULL,
		category TEXT NOT NULL,
		severity TEXT NOT NULL,
		subject TEXT,
		detail TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_check_health_finding_run ON check_health_finding(run_at);
	CREATE INDEX IF NOT EXISTS idx_check_health_finding_acc_name ON check_health_finding(account_number, check_name);
	`
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
		Query: `
			SELECT symbol,
			       'exit date ' || CASE WHEN exit_date <> '' THEN exit_date || ' (' || exit_date_source || ')' ELSE 'unset' END
			       || ' has passed; held ' || trading_days_held || '/' || max_trading_days || ' trading days -- manage-exits should have closed this'
			FROM manage_exit
			WHERE account_number = ? AND status = 'OPEN'
			  AND ( (exit_date <> '' AND exit_date < date('now', '-5 hours'))
			     OR (exit_date = ''  AND trading_days_held >= max_trading_days) )`,
	},
	{
		Name:        "exit_date_not_from_fill",
		Category:    "job_lifecycle",
		Severity:    "WARNING",
		Description: "An OPEN position's exit date is still an estimate more than a day after entry: it should have been corrected from the entry order's fill date (manage-exits does this from the broker's order list).",
		Query: `
			SELECT symbol,
			       'exit date ' || CASE WHEN exit_date <> '' THEN exit_date ELSE 'unset' END
			       || ' is still ' || CASE WHEN exit_date_source = '' THEN 'unset' ELSE exit_date_source END
			       || ', not from the fill of entry order ' || IFNULL(entry_order_id, '?')
			FROM manage_exit
			WHERE account_number = ? AND status = 'OPEN' AND exit_date_source <> 'fill'
			  AND entry_time <= datetime('now', '-1 day')`,
	},
	{
		Name:        "stuck_pending_fill",
		Category:    "job_lifecycle",
		Severity:    "WARNING",
		Description: "An entry order the broker accepted was never confirmed filled or cancelled after 3 days.",
		Query: `
			SELECT symbol, 'entry order ' || IFNULL(entry_order_id, '?') || ' accepted ' || entry_time || ' and still PENDING_FILL (provisional exit date ' || exit_date || ')'
			FROM manage_exit
			WHERE account_number = ? AND status = 'PENDING_FILL' AND entry_time <= datetime('now', '-3 days')`,
	},
	{
		Name:        "duplicate_open_trade",
		Category:    "data_integrity",
		Severity:    "WARNING",
		Description: "More than one OPEN manage_exit row exists for the same symbol in the same account.",
		Query: `
			SELECT symbol, COUNT(*) || ' OPEN manage_exit rows for the same symbol'
			FROM manage_exit
			WHERE account_number = ? AND status = 'OPEN'
			GROUP BY symbol
			HAVING COUNT(*) > 1`,
	},
	{
		Name:        "stuck_pending_staged_order",
		Category:    "job_lifecycle",
		Severity:    "WARNING",
		Description: "A stage_order row has been PENDING for more than 2 days -- submit-orders has not picked it up.",
		Query: `
			SELECT symbol, 'staged ' || staged_at || ' still PENDING'
			FROM stage_order
			WHERE account_number = ? AND status = 'PENDING' AND staged_at <= datetime('now', '-2 days')`,
	},
	{
		Name:        "recent_rejected_order",
		Category:    "job_lifecycle",
		Severity:    "WARNING",
		Description: "A staged order was rejected by submit-orders in the last 3 days.",
		Query: `
			SELECT symbol, 'rejected: ' || IFNULL(reject_reason, 'unknown reason') || ' (staged ' || staged_at || ')'
			FROM stage_order
			WHERE account_number = ? AND status = 'REJECTED' AND staged_at >= datetime('now', '-3 days')`,
	},
	{
		Name:        "stale_account_snapshot",
		Category:    "data_freshness",
		Severity:    "WARNING",
		Description: "The local account_state mirror for this account has not been refreshed by account_state.Sync in over 20 hours.",
		Query: `
			SELECT 'account', 'last account_state sync was ' || fetched_at || ' (>20h old)'
			FROM account_state_account
			WHERE account_number = ? AND fetched_at <= datetime('now', '-20 hours')`,
	},
	{
		Name:        "negative_available_funds",
		Category:    "risk",
		Severity:    "CRITICAL",
		Description: "The account's available funds for trading are negative.",
		Query: `
			SELECT 'cash', 'available funds are $' || printf('%.2f', available_funds)
			FROM account_state_account
			WHERE account_number = ? AND available_funds < 0`,
	},
	{
		Name:        "position_concentration",
		Category:    "risk",
		Severity:    "WARNING",
		Description: "A single held position is more than 50% of the account's liquidation value.",
		Query: `
			SELECT p.symbol,
			       '$' || printf('%.2f', p.market_value) || ' is ' || printf('%.1f', 100.0*p.market_value/a.liquidation_value) || '% of liquidation value'
			FROM account_state_position p
			JOIN account_state_account a ON a.account_number = p.account_number
			WHERE p.account_number = ? AND p.long_quantity > 0 AND a.liquidation_value > 0
			  AND p.market_value > 0.5 * a.liquidation_value`,
	},
	{
		Name:        "orphan_active_trade",
		Category:    "data_integrity",
		Severity:    "WARNING",
		Description: "manage_exit marks a symbol OPEN but the local broker mirror shows no held quantity for it.",
		Query: `
			SELECT me.symbol, 'OPEN in manage_exit but no matching position with quantity>0 in the broker mirror'
			FROM manage_exit me
			WHERE me.account_number = ? AND me.status = 'OPEN'
			  AND NOT EXISTS (
			    SELECT 1 FROM account_state_position p
			    WHERE p.account_number = me.account_number AND p.symbol = me.symbol AND p.long_quantity > 0
			  )`,
	},
}

// seedDefaultHealthChecks installs/updates the built-in checks by name. The
// UPDATE clause deliberately omits `enabled`: an operator's on/off choice
// for a given check must survive a code upgrade that only changes its query
// or description.
func seedDefaultHealthChecks(db *sql.DB) error {
	now := time.Now().UTC()
	for _, c := range defaultHealthChecks {
		_, err := db.Exec(`
			INSERT INTO check_health (name, category, severity, enabled, query, description, created_at)
			VALUES (?, ?, ?, 1, ?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET
				category = excluded.category,
				severity = excluded.severity,
				query = excluded.query,
				description = excluded.description`,
			c.Name, c.Category, c.Severity, c.Query, c.Description, now)
		if err != nil {
			return fmt.Errorf("seed health check %q: %w", c.Name, err)
		}
	}
	return nil
}
