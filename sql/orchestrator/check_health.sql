-- Statements of pkg/orchestrator/check_health, read with orchestratorsql.Q("check_health/<name>").
-- Each starts with a "-- name:" line.

-- name: check_duplicate_open_trade
SELECT symbol, COUNT(*) || ' OPEN manage_exit rows for the same symbol'
			FROM manage_exit
			WHERE account_number = ? AND status = 'OPEN'
			GROUP BY symbol
			HAVING COUNT(*) > 1

-- name: check_exit_date_not_from_fill
SELECT symbol,
			       'exit date ' || CASE WHEN exit_date <> '' THEN exit_date ELSE 'unset' END
			       || ' is still ' || CASE WHEN exit_date_source = '' THEN 'unset' ELSE exit_date_source END
			       || ', not from the fill of entry order ' || IFNULL(entry_order_id, '?')
			FROM manage_exit
			WHERE account_number = ? AND status = 'OPEN' AND exit_date_source <> 'fill'
			  AND entry_time <= datetime('now', '-1 day')

-- name: check_negative_available_funds
SELECT 'cash', 'available funds are $' || printf('%.2f', available_funds)
			FROM account_state_account
			WHERE account_number = ? AND available_funds < 0

-- name: check_orphan_active_trade
SELECT me.symbol, 'OPEN in manage_exit but no matching position with quantity>0 in the broker mirror'
			FROM manage_exit me
			WHERE me.account_number = ? AND me.status = 'OPEN'
			  AND NOT EXISTS (
			    SELECT 1 FROM account_state_position p
			    WHERE p.account_number = me.account_number AND p.symbol = me.symbol AND p.long_quantity > 0
			  )

-- name: check_position_concentration
SELECT p.symbol,
			       '$' || printf('%.2f', p.market_value) || ' is ' || printf('%.1f', 100.0*p.market_value/a.liquidation_value) || '% of liquidation value'
			FROM account_state_position p
			JOIN account_state_account a ON a.account_number = p.account_number
			WHERE p.account_number = ? AND p.long_quantity > 0 AND a.liquidation_value > 0
			  AND p.market_value > 0.5 * a.liquidation_value

-- name: check_recent_rejected_order
SELECT symbol, 'rejected: ' || IFNULL(reject_reason, 'unknown reason') || ' (staged ' || staged_at || ')'
			FROM stage_order
			WHERE account_number = ? AND status = 'REJECTED' AND staged_at >= datetime('now', '-3 days')

-- name: check_stale_account_snapshot
SELECT 'account', 'last account_state sync was ' || fetched_at || ' (>20h old)'
			FROM account_state_account
			WHERE account_number = ? AND fetched_at <= datetime('now', '-20 hours')

-- name: check_stale_time_expired_trade
SELECT symbol,
			       'exit date ' || CASE WHEN exit_date <> '' THEN exit_date || ' (' || exit_date_source || ')' ELSE 'unset' END
			       || ' has passed; held ' || trading_days_held || '/' || max_trading_days || ' trading days -- manage-exits should have closed this'
			FROM manage_exit
			WHERE account_number = ? AND status = 'OPEN'
			  AND ( (exit_date <> '' AND exit_date < date('now', '-5 hours'))
			     OR (exit_date = ''  AND trading_days_held >= max_trading_days) )

-- name: check_stuck_pending_fill
SELECT symbol, 'entry order ' || IFNULL(entry_order_id, '?') || ' accepted ' || entry_time || ' and still PENDING_FILL (provisional exit date ' || exit_date || ')'
			FROM manage_exit
			WHERE account_number = ? AND status = 'PENDING_FILL' AND entry_time <= datetime('now', '-3 days')

-- name: check_stuck_pending_staged_order
SELECT symbol, 'staged ' || staged_at || ' still PENDING'
			FROM stage_order
			WHERE account_number = ? AND status = 'PENDING' AND staged_at <= datetime('now', '-2 days')

-- name: ensure_schema
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

-- name: get_recent_findings
SELECT id, run_at, account_number, check_name, category, severity, subject, detail
		FROM check_health_finding
		WHERE account_number = ? OR ? = ''
		ORDER BY run_at DESC, id DESC
		LIMIT ?

-- name: latest_position_audits
SELECT p.run_at, p.account_number, p.symbol, p.quantity, p.average_price, p.strategy, p.entry_order_id,
		p.entry_fill_date, p.exit_date, p.exit_date_source, p.expected_exit_date, p.take_profit, p.expected_take_profit,
		p.stop_loss, p.expected_stop_loss, p.status, p.issues
		FROM check_health_position p
		WHERE p.run_at = (SELECT MAX(x.run_at) FROM check_health_position x WHERE x.account_number = p.account_number)
		AND p.symbol <> ''
		ORDER BY CASE p.status WHEN 'CRITICAL' THEN 0 WHEN 'WARNING' THEN 1 ELSE 2 END, p.account_number, p.symbol

-- name: list_checks
SELECT id, name, category, severity, enabled, query, description, created_at
		FROM check_health ORDER BY category, name

-- name: load_status
SELECT id, account_number, timestamp, total_positions, protected_positions, unprotected_positions,
		       orphan_orders, status, IFNULL(notes, ''), critical_findings, warning_findings
		FROM check_health_audit
		WHERE id IN (SELECT MAX(id) FROM check_health_audit GROUP BY account_number)
		ORDER BY account_number

-- name: load_status_2
SELECT f.id, f.run_at, f.account_number, f.check_name, f.category, f.severity, IFNULL(f.subject, ''), f.detail
		FROM check_health_finding f
		WHERE f.run_at = (SELECT MAX(x.run_at) FROM check_health_finding x WHERE x.account_number = f.account_number)
		ORDER BY CASE f.severity WHEN 'CRITICAL' THEN 0 ELSE 1 END, f.account_number, f.check_name, f.subject
		LIMIT 200

-- name: position_schema
CREATE TABLE IF NOT EXISTS check_health_position (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_at DATETIME NOT NULL,
	account_number TEXT NOT NULL,
	symbol TEXT NOT NULL,
	quantity REAL NOT NULL,
	average_price REAL NOT NULL,
	strategy TEXT NOT NULL DEFAULT '',
	entry_order_id TEXT NOT NULL DEFAULT '',
	entry_fill_date TEXT NOT NULL DEFAULT '',
	exit_date TEXT NOT NULL DEFAULT '',
	exit_date_source TEXT NOT NULL DEFAULT '',
	expected_exit_date TEXT NOT NULL DEFAULT '',
	take_profit REAL NOT NULL DEFAULT 0,
	expected_take_profit REAL NOT NULL DEFAULT 0,
	stop_loss REAL NOT NULL DEFAULT 0,
	expected_stop_loss REAL NOT NULL DEFAULT 0,
	status TEXT NOT NULL,
	issues TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_check_health_position_run ON check_health_position(account_number, run_at);

-- name: record_audit
INSERT INTO check_health_audit (
		account_number, timestamp, total_positions, protected_positions,
		unprotected_positions, orphan_orders, status, notes,
		critical_findings, warning_findings
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)

-- name: record_finding
INSERT INTO check_health_finding (run_at, account_number, check_name, category, severity, subject, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?)

-- name: record_position_audit
INSERT INTO check_health_position (run_at, account_number, symbol, quantity, average_price, strategy,
		entry_order_id, entry_fill_date, exit_date, exit_date_source, expected_exit_date, take_profit, expected_take_profit,
		stop_loss, expected_stop_loss, status, issues) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)

-- name: seed_default_health_checks
INSERT INTO check_health (name, category, severity, enabled, query, description, created_at)
			VALUES (?, ?, ?, 1, ?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET
				category = excluded.category,
				severity = excluded.severity,
				query = excluded.query,
				description = excluded.description

-- name: set_enabled
UPDATE check_health SET enabled = ? WHERE name = ?
