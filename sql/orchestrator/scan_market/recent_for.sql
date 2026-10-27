SELECT created_at, as_of_date, next_session, stack_id, strategy_id, symbol, close, buy_limit,
       take_profit, stop_loss, allocation_pct, hold_days, status, notes
FROM scan_market_buy_signal WHERE as_of_date >= ? ORDER BY as_of_date DESC, id DESC
