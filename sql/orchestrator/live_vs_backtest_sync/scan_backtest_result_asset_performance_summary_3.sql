SELECT t.strategy_id, COALESCE(t.symbol, ''), COALESCE(t.exit_date, ''), CASE WHEN t.net_pnl > 0 THEN 1 ELSE 0 END, t.return_pct
		FROM trades t JOIN performance_summary p ON p.run_id = t.run_id AND p.strategy_id = t.strategy_id
