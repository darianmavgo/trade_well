SELECT equity FROM risk_equity WHERE account_number = ? AND session_date < ? ORDER BY session_date DESC LIMIT 1
