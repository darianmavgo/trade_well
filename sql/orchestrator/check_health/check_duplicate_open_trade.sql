SELECT symbol, COUNT(*) || ' OPEN manage_exit rows for the same symbol'
			FROM manage_exit
			WHERE account_number = ? AND status = 'OPEN'
			GROUP BY symbol
			HAVING COUNT(*) > 1
