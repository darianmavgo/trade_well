-- Stage 9: the running count of live trades and wins after each close, per strategy.
DROP TABLE IF EXISTS winrate_progress_count;
CREATE TABLE winrate_progress_count AS
SELECT strategy_id, symbol, exit_date, win, pnl_pct,
	ROW_NUMBER() OVER (PARTITION BY strategy_id ORDER BY exit_date, id) AS n,
	SUM(win) OVER (PARTITION BY strategy_id ORDER BY exit_date, id) AS wins
FROM winrate_trades;
