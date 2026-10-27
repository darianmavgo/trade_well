SELECT account_number, strategy_id, symbol, greenlit, buy_order_id, sell_order_id,
		buy_price, sell_price, qty, fees, cash_pnl, return_pct, opened_at, closed_at, manage_status
		FROM live_vs_backtest_report_round_trip ORDER BY opened_at, symbol, account_number
