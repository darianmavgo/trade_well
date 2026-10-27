SELECT strategy_id, fills, avg_entry_slippage_bps, avg_exit_slippage_bps,
		worst_entry_slippage_bps, avg_entry_vs_limit_bps, avg_fill_delay_sessions, total_fees
		FROM live_vs_backtest_slippage ORDER BY strategy_id
