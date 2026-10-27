INSERT INTO live_vs_backtest_winrate (
	strategy_id, is_start, is_end, oos_start, oos_end,
	is_trades, is_win_rate, oos_trades, oos_win_rate, evaluated_at, synced_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(strategy_id) DO UPDATE SET
	is_start = excluded.is_start, is_end = excluded.is_end,
	oos_start = excluded.oos_start, oos_end = excluded.oos_end,
	is_trades = excluded.is_trades, is_win_rate = excluded.is_win_rate,
	oos_trades = excluded.oos_trades, oos_win_rate = excluded.oos_win_rate,
	evaluated_at = excluded.evaluated_at, synced_at = excluded.synced_at
