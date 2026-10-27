SELECT account_number, COALESCE(account_type, ''), COALESCE(available_funds, 0), COALESCE(raw_json, ''), fetched_at FROM account_state_account ORDER BY account_number
