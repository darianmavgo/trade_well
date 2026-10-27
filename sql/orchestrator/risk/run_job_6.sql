INSERT INTO risk_equity (account_number, session_date, equity, recorded_at) VALUES (?,?,?,?)
ON CONFLICT(account_number, session_date) DO UPDATE SET equity = excluded.equity, recorded_at = excluded.recorded_at
