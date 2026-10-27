SELECT p.symbol,
			       '$' || printf('%.2f', p.market_value) || ' is ' || printf('%.1f', 100.0*p.market_value/a.liquidation_value) || '% of liquidation value'
			FROM account_state_position p
			JOIN account_state_account a ON a.account_number = p.account_number
			WHERE p.account_number = ? AND p.long_quantity > 0 AND a.liquidation_value > 0
			  AND p.market_value > 0.5 * a.liquidation_value
