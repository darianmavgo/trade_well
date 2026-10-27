SELECT symbol, asset_type, long_quantity, short_quantity, average_price, market_value
		FROM account_state_position WHERE account_number = ?
