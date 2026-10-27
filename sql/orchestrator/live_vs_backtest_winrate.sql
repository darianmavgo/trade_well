-- Statements of pkg/orchestrator/live_vs_backtest_winrate, read with orchestratorsql.Q("live_vs_backtest_winrate/<name>").
-- Each starts with a "-- name:" line.

-- name: compare
DROP TABLE IF EXISTS winrate_ids; CREATE TABLE winrate_ids (strategy_id TEXT PRIMARY KEY)

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS live_vs_backtest_winrate (
	strategy_id TEXT PRIMARY KEY,
	is_start TEXT, is_end TEXT, oos_start TEXT, oos_end TEXT,
	is_trades INTEGER NOT NULL DEFAULT 0,
	is_win_rate REAL,
	oos_trades INTEGER NOT NULL DEFAULT 0,
	oos_win_rate REAL,
	evaluated_at TEXT,
	synced_at TEXT NOT NULL
)

-- name: ensure_schema_2
SELECT COUNT(*) FROM pragma_table_info('live_vs_backtest_winrate') WHERE name = ?

-- name: export_baseline
SELECT strategy_id, is_trades, is_win_rate, oos_trades, oos_win_rate, COALESCE(all_trades, 0), all_win_rate,
		COALESCE(is_start, ''), COALESCE(is_end, ''), COALESCE(oos_start, ''), COALESCE(oos_end, ''), COALESCE(evaluated_at, ''), COALESCE(all_evaluated_at, '')
		FROM live_vs_backtest_winrate
		WHERE (is_win_rate IS NOT NULL OR oos_win_rate IS NOT NULL OR all_win_rate IS NOT NULL)
		ORDER BY strategy_id

-- name: export_baseline_2
SELECT strategy_id, pass, symbol, exit_date, win, return_pct FROM live_vs_backtest_deployed_trade ORDER BY strategy_id, pass, exit_date, symbol

-- name: has_table
SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?

-- name: load_baseline
DROP TABLE IF EXISTS winrate_baseline;
CREATE TABLE winrate_baseline (strategy_id TEXT, is_trades INTEGER, is_win_rate REAL, oos_trades INTEGER, oos_win_rate REAL,
	all_trades INTEGER, all_win_rate REAL, is_start TEXT, is_end TEXT, oos_start TEXT, oos_end TEXT, evaluated_at TEXT, all_evaluated_at TEXT);
DROP TABLE IF EXISTS winrate_baseline_trade;
CREATE TABLE winrate_baseline_trade (strategy_id TEXT, pass TEXT, symbol TEXT, exit_date TEXT, win INTEGER, return_pct REAL)

-- name: read_compare
SELECT strategy_id, verdict, greenlit, is_win_rate, COALESCE(is_trades, 0), oos_win_rate, COALESCE(oos_trades, 0),
	all_win_rate, COALESCE(all_trades, 0),
	live_win_rate, live_wins, live_n, live_fills, live_ci_low, live_ci_high, model_win_rate, model_n,
	COALESCE(base_kind, ''), live_minus_base, live_minus_model, live_avg_return, open_n, pending_n,
	COALESCE(first_close, ''), COALESCE(last_close, ''), COALESCE(evaluated_at, all_evaluated_at, ''), COALESCE(source, '')
FROM winrate_compare
WHERE greenlit OR live_n > 0 OR open_n > 0 OR pending_n > 0
ORDER BY CASE verdict WHEN 'BELOW' THEN 0 WHEN 'IN_LINE' THEN 1 WHEN 'ABOVE' THEN 2 WHEN 'TOO_EARLY' THEN 3 ELSE 4 END,
	live_n DESC, strategy_id

-- name: read_compare_2
SELECT strategy_id, n, COALESCE(exit_date, ''), COALESCE(symbol, ''), win, COALESCE(pnl_pct, 0), wins, rate, ci_low, ci_high
		FROM winrate_progress ORDER BY strategy_id, n

-- name: read_compare_3
SELECT strategy_id, pass, n, COALESCE(exit_date, ''), COALESCE(symbol, ''), win, COALESCE(return_pct, 0), rate
		FROM winrate_bt_progress ORDER BY strategy_id, pass, n

-- name: read_recent
SELECT strategy_id, COALESCE(account_number, ''), symbol, status, exit_reason, exit_reason_code, entry_time, exit_time,
		COALESCE(quantity, 0), COALESCE(entry_price, 0), COALESCE(entry_fill_price, 0), COALESCE(exit_fill_price, 0), COALESCE(exit_price, 0), COALESCE(pnl, 0), COALESCE(pnl_pct, 0), stop_loss
		FROM winrate_recent ORDER BY exit_time DESC, strategy_id, account_number

-- name: recent
DROP TABLE IF EXISTS winrate_window; CREATE TABLE winrate_window (days INTEGER)

-- name: refresh
SELECT COUNT(*) FROM live_vs_backtest_winrate
