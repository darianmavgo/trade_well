-- Statements of pkg/orchestrator/live_vs_backtest_report, read with orchestratorsql.Q("live_vs_backtest_report/<name>").
-- Each starts with a "-- name:" line.

-- name: gap_notes
SELECT COUNT(*) FROM stage_order

-- name: gap_notes_2
SELECT COUNT(*) FROM manage_exit

-- name: ledger_gap_notes
SELECT account_number, symbol, exit_fill_price FROM live_vs_backtest_trade WHERE exit_fill_price = 0 AND live_outcome IN ('OPEN', 'CLOSED')

-- name: load_decisions
SELECT account_number, strategy_id, symbol, as_of_date, live_outcome, model_outcome,
		live_return_pct, model_return_pct, entry_slippage_bps, exit_slippage_bps,
		signal_match, outcome_match, live_exit_reason, model_exit_reason, skip_reason
		FROM live_vs_backtest_trade

-- name: load_exits
SELECT id, account_number, symbol,
		CASE WHEN COALESCE(strategy_id, '') != '' THEN strategy_id ELSE strategy_name END,
		COALESCE(entry_order_id, ''), status
		FROM manage_exit

-- name: load_page
SELECT key, value FROM live_vs_backtest_report_meta

-- name: load_page_2
SELECT severity, message FROM live_vs_backtest_report_note ORDER BY id

-- name: load_page_3
SELECT SUM(cash_pnl) FROM live_vs_backtest_report_round_trip WHERE greenlit = 1 AND sell_order_id != ''

-- name: load_slippage
SELECT strategy_id, fills, avg_entry_slippage_bps, avg_exit_slippage_bps,
		worst_entry_slippage_bps, avg_entry_vs_limit_bps, avg_fill_delay_sessions, total_fees
		FROM live_vs_backtest_slippage ORDER BY strategy_id

-- name: load_strategies
SELECT strategy_id,
		SUM(CASE WHEN sell_order_id != '' AND cash_pnl IS NOT NULL THEN 1 ELSE 0 END),
		SUM(CASE WHEN sell_order_id != '' THEN cash_pnl END),
		MAX(greenlit)
		FROM live_vs_backtest_report_round_trip GROUP BY strategy_id ORDER BY strategy_id

-- name: load_trips
SELECT account_number, strategy_id, symbol, greenlit, buy_order_id, sell_order_id,
		buy_price, sell_price, qty, fees, cash_pnl, return_pct, opened_at, closed_at, manage_status
		FROM live_vs_backtest_report_round_trip ORDER BY opened_at, symbol, account_number

-- name: report_ddl
CREATE TABLE live_vs_backtest_report_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE live_vs_backtest_report_note (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	severity TEXT NOT NULL,
	message TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS live_vs_backtest_report_round_trip (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	account_number TEXT NOT NULL,
	strategy_id TEXT NOT NULL DEFAULT '',
	symbol TEXT NOT NULL,
	greenlit INTEGER NOT NULL DEFAULT 0,
	buy_order_id TEXT NOT NULL,
	sell_order_id TEXT NOT NULL DEFAULT '',
	buy_price REAL NOT NULL,
	sell_price REAL NOT NULL DEFAULT 0,
	qty REAL NOT NULL,
	fees REAL NOT NULL DEFAULT 0,
	cash_pnl REAL,
	return_pct REAL,
	opened_at TEXT NOT NULL DEFAULT '',
	closed_at TEXT NOT NULL DEFAULT '',
	manage_exit_id INTEGER NOT NULL DEFAULT 0,
	manage_status TEXT NOT NULL DEFAULT ''
);

-- name: run
SELECT COUNT(*) FROM live_vs_backtest_trade

-- name: run_2
DELETE FROM live_vs_backtest_slippage
