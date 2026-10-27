SELECT account_number, symbol, COALESCE(long_quantity, 0) - COALESCE(short_quantity, 0),
       COALESCE(market_value, 0), COALESCE(market_value, 0) - (COALESCE(long_quantity, 0) - COALESCE(short_quantity, 0)) * COALESCE(average_price, 0),
       COALESCE(exit_date, '')
FROM account_state_position WHERE COALESCE(long_quantity, 0) <> 0 OR COALESCE(short_quantity, 0) <> 0 ORDER BY market_value DESC
