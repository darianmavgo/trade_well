-- Stage 4c: live results per strategy, counted in decisions (live_n, live_wins) with the account
-- fills behind them (live_fills).
DROP TABLE IF EXISTS winrate_live;
CREATE TABLE winrate_live AS
SELECT d.strategy_id,
	COUNT(*) AS live_n,
	SUM(d.win) AS live_wins,
	SUM(d.fills) AS live_fills,
	AVG(d.pnl_pct) AS live_avg_return,
	MIN(substr(d.exit_time, 1, 10)) AS first_close,
	MAX(substr(d.exit_time, 1, 10)) AS last_close
FROM winrate_decisions d
GROUP BY d.strategy_id;
