SELECT 'cash', 'available funds are $' || printf('%.2f', available_funds)
			FROM account_state_account
			WHERE account_number = ? AND available_funds < 0
