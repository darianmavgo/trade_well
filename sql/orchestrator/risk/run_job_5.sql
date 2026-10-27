SELECT COALESCE(SUM(market_value), 0) FROM account_state_position WHERE account_number = ? AND long_quantity > 0
