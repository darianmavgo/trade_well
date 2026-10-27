INSERT INTO live_vs_backtest_winrate (strategy_id, all_trades, all_win_rate, all_evaluated_at, synced_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(strategy_id) DO UPDATE SET
	all_trades = excluded.all_trades, all_win_rate = excluded.all_win_rate,
	all_evaluated_at = excluded.all_evaluated_at, synced_at = excluded.synced_at
