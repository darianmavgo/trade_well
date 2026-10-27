-- Stage 20: closed live trades in the last N days (N is in winrate_window), newest first.
-- A stop loss is exit_reason_code STOP_LOSS. A position closed through a broker stop shows the
-- same way as one the exit manager sold.
DROP TABLE IF EXISTS winrate_recent;
CREATE TABLE winrate_recent AS
SELECT COALESCE(NULLIF(strategy_id, ''), strategy_name) AS strategy_id,
	account_number, symbol, status, COALESCE(exit_reason, '') AS exit_reason, COALESCE(exit_reason_code, '') AS exit_reason_code,
	COALESCE(entry_time, '') AS entry_time, COALESCE(exit_time, '') AS exit_time, exit_date,
	quantity, entry_price, entry_fill_price, exit_fill_price, exit_price, pnl, pnl_pct,
	CASE WHEN exit_reason_code = 'STOP_LOSS' OR status = 'CLOSED_STOP_LOSS' THEN 1 ELSE 0 END AS stop_loss
FROM manage_exit
WHERE status LIKE 'CLOSED%'
	AND substr(exit_time, 1, 10) >= date('now', '-' || (SELECT days FROM winrate_window) || ' days');
