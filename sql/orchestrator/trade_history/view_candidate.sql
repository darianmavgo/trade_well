-- The trades that actually happened, read from the positions manage_exits closed. A closed
-- manage_exit row is a trade when it is attributed to a strategy, has both order ids, and has
-- both fills recorded from the broker; entry and exit prices of 0 mean no fill was recorded
-- (cancelled, rejected, or still working). If the broker mirror holds the entry order and it is
-- not FILLED, the row is not a trade either. An entry order the mirror has not fetched yet is
-- given the benefit of the doubt: the recorded fills are the evidence.
DROP VIEW IF EXISTS trade_history_candidate;
CREATE VIEW trade_history_candidate AS
SELECT COALESCE(NULLIF(m.strategy_id, ''), m.strategy_name) AS strategy_id,
	m.account_number, m.symbol, m.entry_order_id, m.exit_order_id,
	m.entry_time, m.exit_time,
	COALESCE(NULLIF(m.filled_qty, 0), m.quantity) AS quantity,
	m.entry_fill_price, m.exit_fill_price, m.fees, m.pnl, m.pnl_pct,
	m.exit_reason_code, m.id AS manage_exit_id
FROM manage_exit m
WHERE m.status LIKE 'CLOSED%'
	AND COALESCE(NULLIF(m.strategy_id, ''), m.strategy_name, '') <> ''
	AND COALESCE(m.entry_order_id, '') <> ''
	AND COALESCE(m.exit_order_id, '') <> ''
	AND m.entry_fill_price > 0
	AND m.exit_fill_price > 0
	AND m.exit_time IS NOT NULL
	AND NOT EXISTS (SELECT 1 FROM account_state_order o WHERE o.order_id = m.entry_order_id AND o.status <> 'FILLED');
