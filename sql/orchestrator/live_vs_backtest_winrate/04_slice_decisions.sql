-- Stage 4a: live decisions. One decision is one strategy buying one symbol on one day, however
-- many accounts it was filled in; it is a win when its P&L summed over those fills is positive.
-- Counting fills instead would score a single stop loss in three accounts as three losses.
-- Read from trade_history, the trades that really happened (both fills recorded), not from every
-- CLOSED manage_exit row: a cancelled or never-filled entry has no P&L and must not count as a loss.
DROP TABLE IF EXISTS winrate_decisions;
CREATE TABLE winrate_decisions AS
SELECT strategy_id,
	symbol, substr(entry_time, 1, 10) AS entry_day,
	COUNT(*) AS fills, SUM(pnl) AS pnl, AVG(pnl_pct) AS pnl_pct,
	MAX(exit_time) AS exit_time, MIN(id) AS id,
	CASE WHEN SUM(pnl) > 0 THEN 1 ELSE 0 END AS win
FROM trade_history
GROUP BY 1, 2, 3;
