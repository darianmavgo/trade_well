SELECT account_number, symbol, COALESCE(long_quantity, 0), fetched_at FROM account_state_position
