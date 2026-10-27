-- The held-out pass of the same run (<run>/oos/*.db).
INSERT INTO live_vs_backtest_winrate (strategy_id, oos_start, oos_end, oos_trades, oos_win_rate, evaluated_at, synced_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(strategy_id) DO UPDATE SET
	oos_start = excluded.oos_start, oos_end = excluded.oos_end,
	oos_trades = excluded.oos_trades, oos_win_rate = excluded.oos_win_rate,
	evaluated_at = excluded.evaluated_at, synced_at = excluded.synced_at
