-- Statements of pkg/orchestrator/clear_stale, read with orchestratorsql.Q("clear_stale/<name>").
-- Each starts with a "-- name:" line.

-- name: select_orphan_trades
SELECT me.id, me.account_number, me.symbol, me.strategy_name, me.strategy_id, IFNULL(me.entry_filled_at, ''),
		       IFNULL((SELECT a.fetched_at FROM account_state_account a WHERE a.account_number = me.account_number), '')
		FROM manage_exit me
		WHERE me.status = 'OPEN'
		  AND NOT EXISTS (
		    SELECT 1 FROM account_state_position p
		    WHERE p.account_number = me.account_number AND p.symbol = me.symbol AND p.long_quantity > 0
		  )
		ORDER BY me.id

-- name: select_stale_orders
SELECT id, account_number, symbol, strategy_name, IFNULL(reject_reason, ''), staged_at
		FROM stage_order
		WHERE status = ?
		ORDER BY id
