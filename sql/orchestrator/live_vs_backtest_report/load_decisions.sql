SELECT account_number, strategy_id, symbol, as_of_date, live_outcome, model_outcome,
		live_return_pct, model_return_pct, entry_slippage_bps, exit_slippage_bps,
		signal_match, outcome_match, live_exit_reason, model_exit_reason, skip_reason
		FROM live_vs_backtest_trade
