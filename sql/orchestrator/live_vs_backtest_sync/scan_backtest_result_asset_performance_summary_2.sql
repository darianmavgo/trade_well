SELECT strategy_id, COALESCE(total_trades, 0), win_rate, start_date, end_date FROM performance_summary
