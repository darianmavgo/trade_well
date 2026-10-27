SELECT me.symbol, 'OPEN in manage_exit but no matching position with quantity>0 in the broker mirror'
			FROM manage_exit me
			WHERE me.account_number = ? AND me.status = 'OPEN'
			  AND NOT EXISTS (
			    SELECT 1 FROM account_state_position p
			    WHERE p.account_number = me.account_number AND p.symbol = me.symbol AND p.long_quantity > 0
			  )
