-- Run on a backtest result ASSET (<run>/*.db or <run>/oos/*.db): one summary row per strategy.
-- Only these columns are loaded into Turso.
SELECT strategy_id, start_date, end_date, COALESCE(total_trades, 0), win_rate, created_at
FROM performance_summary
