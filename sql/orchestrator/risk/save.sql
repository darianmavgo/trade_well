INSERT INTO risk_state (account_number, assessed_at, session_date, equity, peak, drawdown, day_change, gross, scale, halt_entries, max_gross, reasons,
	max_order_pct, max_orders_per_day, max_positions)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(account_number) DO UPDATE SET assessed_at=excluded.assessed_at, session_date=excluded.session_date,
	equity=excluded.equity, peak=excluded.peak, drawdown=excluded.drawdown, day_change=excluded.day_change,
	gross=excluded.gross, scale=excluded.scale, halt_entries=excluded.halt_entries, max_gross=excluded.max_gross, reasons=excluded.reasons,
	max_order_pct=excluded.max_order_pct, max_orders_per_day=excluded.max_orders_per_day, max_positions=excluded.max_positions
