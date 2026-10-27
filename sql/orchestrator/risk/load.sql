SELECT assessed_at, session_date, equity, peak, drawdown, day_change, gross, scale, halt_entries, max_gross, reasons,
	max_order_pct, max_orders_per_day, max_positions
FROM risk_state WHERE account_number = ?
