SELECT strategy_id, symbol, as_of_date, entry_slippage_bps, exit_slippage_bps,
		fill_delay_sessions, fees, entry_fill_price, model_entry_price
		FROM live_vs_backtest_trade
		WHERE entry_slippage_bps IS NOT NULL
		ORDER BY entry_slippage_bps DESC
		LIMIT 8
