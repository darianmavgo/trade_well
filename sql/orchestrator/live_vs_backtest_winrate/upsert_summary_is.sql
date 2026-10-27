-- The default backtest run covers the in-sample window. Runs are applied oldest first, so the
-- newest run of a strategy wins.
INSERT INTO live_vs_backtest_winrate (strategy_id, is_start, is_end, is_trades, is_win_rate, evaluated_at, synced_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(strategy_id) DO UPDATE SET
	is_start = excluded.is_start, is_end = excluded.is_end,
	is_trades = excluded.is_trades, is_win_rate = excluded.is_win_rate,
	evaluated_at = excluded.evaluated_at, synced_at = excluded.synced_at
