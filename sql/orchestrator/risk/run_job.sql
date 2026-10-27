SELECT account_number, COALESCE(liquidation_value, 0), fetched_at FROM account_state_account ORDER BY account_number
