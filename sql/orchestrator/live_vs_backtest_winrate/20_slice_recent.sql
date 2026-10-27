-- Stage 20: closed live trades in the last N days (N is in winrate_window), newest first.
-- The trades are the rows of trade_history (the ones that really traded). The descriptive fields
-- trade_history does not keep (exit reason, exit date, planned prices, status) come from the
-- manage_exit row it was captured from. A stop loss is exit_reason_code STOP_LOSS. A position
-- closed through a broker stop shows the same way as one the exit manager sold.
DROP TABLE IF EXISTS winrate_recent;
CREATE TABLE winrate_recent AS
SELECT t.strategy_id,
	t.account_number, t.symbol, COALESCE(m.status, 'CLOSED') AS status, COALESCE(m.exit_reason, '') AS exit_reason, t.exit_reason_code,
	t.entry_time, t.exit_time, COALESCE(m.exit_date, substr(t.exit_time, 1, 10)) AS exit_date,
	t.quantity, COALESCE(m.entry_price, 0) AS entry_price, t.entry_fill_price, t.exit_fill_price, COALESCE(m.exit_price, 0) AS exit_price, t.pnl, t.pnl_pct,
	CASE WHEN t.exit_reason_code = 'STOP_LOSS' OR m.status = 'CLOSED_STOP_LOSS' THEN 1 ELSE 0 END AS stop_loss
FROM trade_history t
LEFT JOIN manage_exit m ON m.id = t.manage_exit_id
WHERE substr(t.exit_time, 1, 10) >= date('now', '-' || (SELECT days FROM winrate_window) || ' days');
