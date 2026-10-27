SELECT 'account', 'last account_state sync was ' || fetched_at || ' (>20h old)'
			FROM account_state_account
			WHERE account_number = ? AND fetched_at <= datetime('now', '-20 hours')
