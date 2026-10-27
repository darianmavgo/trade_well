-- Copies the trades that actually happened into trade_history. A trade already there (same
-- account, entry order and exit order) is left as it was captured, so running this again adds
-- only the trades closed since.
INSERT OR IGNORE INTO trade_history (strategy_id, account_number, symbol, entry_order_id, exit_order_id,
	entry_time, exit_time, quantity, entry_fill_price, exit_fill_price, fees, pnl, pnl_pct,
	exit_reason_code, manage_exit_id, captured_at)
SELECT strategy_id, account_number, symbol, entry_order_id, exit_order_id,
	entry_time, exit_time, quantity, entry_fill_price, exit_fill_price, fees, pnl, pnl_pct,
	exit_reason_code, manage_exit_id, ?
FROM trade_history_candidate;
