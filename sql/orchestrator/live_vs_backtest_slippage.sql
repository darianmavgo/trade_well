-- Statements of pkg/orchestrator/live_vs_backtest_slippage, read with orchestratorsql.Q("live_vs_backtest_slippage/<name>").
-- Each starts with a "-- name:" line.

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS live_vs_backtest_slippage (
	strategy_id TEXT PRIMARY KEY,
	fills INTEGER NOT NULL,
	avg_entry_slippage_bps REAL,
	avg_exit_slippage_bps REAL,
	worst_entry_slippage_bps REAL,
	avg_entry_vs_limit_bps REAL,
	avg_fill_delay_sessions REAL,
	total_fees REAL NOT NULL DEFAULT 0,
	built_at TEXT NOT NULL
)

-- name: run
SELECT COUNT(*) FROM live_vs_backtest_trade

-- name: run_2
DELETE FROM live_vs_backtest_slippage

-- name: run_3
INSERT INTO live_vs_backtest_slippage (
		strategy_id, fills, avg_entry_slippage_bps, avg_exit_slippage_bps, worst_entry_slippage_bps,
		avg_entry_vs_limit_bps, avg_fill_delay_sessions, total_fees, built_at
	)
	SELECT strategy_id,
		SUM(CASE WHEN entry_fill_price > 0 THEN 1 ELSE 0 END),
		AVG(entry_slippage_bps),
		AVG(exit_slippage_bps),
		MAX(entry_slippage_bps),
		AVG(entry_vs_limit_bps),
		AVG(fill_delay_sessions),
		SUM(fees),
		?
	FROM live_vs_backtest_trade
	GROUP BY strategy_id

-- name: run_4
SELECT strategy_id, fills, avg_entry_slippage_bps, avg_exit_slippage_bps,
		worst_entry_slippage_bps, avg_entry_vs_limit_bps, avg_fill_delay_sessions, total_fees
		FROM live_vs_backtest_slippage ORDER BY strategy_id

-- name: worst_fills
SELECT strategy_id, symbol, as_of_date, entry_slippage_bps, exit_slippage_bps,
		fill_delay_sessions, fees, entry_fill_price, model_entry_price
		FROM live_vs_backtest_trade
		WHERE entry_slippage_bps IS NOT NULL
		ORDER BY entry_slippage_bps DESC
		LIMIT 8
