SELECT strategy_id,
		SUM(CASE WHEN sell_order_id != '' AND cash_pnl IS NOT NULL THEN 1 ELSE 0 END),
		SUM(CASE WHEN sell_order_id != '' THEN cash_pnl END),
		MAX(greenlit)
		FROM live_vs_backtest_report_round_trip GROUP BY strategy_id ORDER BY strategy_id
