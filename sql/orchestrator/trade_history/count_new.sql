-- How many trades that actually happened are not in trade_history yet.
SELECT COUNT(*)
FROM trade_history_candidate c
WHERE NOT EXISTS (SELECT 1 FROM trade_history h
	WHERE h.account_number = c.account_number AND h.entry_order_id = c.entry_order_id AND h.exit_order_id = c.exit_order_id);
