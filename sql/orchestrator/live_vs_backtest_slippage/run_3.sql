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
