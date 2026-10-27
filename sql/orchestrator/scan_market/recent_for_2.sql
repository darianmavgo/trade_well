SELECT COALESCE(signal_date, ''), strategy_name, COALESCE(indicator_data, '')
FROM scan_market_signal WHERE signal_type = 'NO_BUY' AND signal_date >= ?
ORDER BY signal_date DESC, strategy_name
