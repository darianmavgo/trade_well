-- One decision is one strategy buying one symbol on one day, however many accounts it was filled
-- in; it is a win when its P&L summed over those fills is positive. Counting fills instead would
-- score a single stop loss in three accounts as three losses. (Same rule as the win-rate
-- comparison's stage 4a, applied to trade_history.)
DROP VIEW IF EXISTS trade_history_decision;
CREATE VIEW trade_history_decision AS
SELECT strategy_id, symbol, substr(entry_time, 1, 10) AS entry_day,
	COUNT(*) AS fills, SUM(pnl) AS pnl, AVG(pnl_pct) AS pnl_pct,
	MAX(exit_time) AS exit_time, MIN(id) AS id,
	CASE WHEN SUM(pnl) > 0 THEN 1 ELSE 0 END AS win
FROM trade_history
GROUP BY strategy_id, symbol, substr(entry_time, 1, 10);
