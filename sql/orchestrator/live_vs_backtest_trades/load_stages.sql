SELECT id, COALESCE(signal_id, 0), account_number, strategy_name, symbol,
		quantity, entry_price, take_profit_price, stop_loss_price, max_trading_days, status,
		COALESCE(reject_reason, ''), COALESCE(order_id, ''), staged_at
		FROM stage_order
