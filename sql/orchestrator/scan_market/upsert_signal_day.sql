INSERT INTO scan_market_signal (symbol, strategy_name, signal_type, price, indicator_data, generated_at, status, signal_date)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(strategy_name, signal_date) DO UPDATE SET
			symbol=excluded.symbol,
			signal_type=excluded.signal_type,
			price=excluded.price,
			indicator_data=excluded.indicator_data,
			generated_at=excluded.generated_at,
			status=excluded.status
