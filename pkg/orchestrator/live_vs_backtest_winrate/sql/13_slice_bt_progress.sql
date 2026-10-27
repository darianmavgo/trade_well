-- Stage 13: the running win rate after each backtest trade, per strategy and pass (is / oos),
-- the same curve the live trades get.
DROP TABLE IF EXISTS winrate_bt_progress_count;
CREATE TABLE winrate_bt_progress_count AS
SELECT strategy_id, pass, symbol, exit_date, win, return_pct,
	ROW_NUMBER() OVER (PARTITION BY strategy_id, pass ORDER BY exit_date, symbol) AS n,
	SUM(win) OVER (PARTITION BY strategy_id, pass ORDER BY exit_date, symbol) AS wins
FROM winrate_bt_trades;

DROP TABLE IF EXISTS winrate_bt_progress;
CREATE TABLE winrate_bt_progress AS
SELECT strategy_id, pass, n, symbol, exit_date, win, return_pct, wins, 1.0 * wins / n AS rate
FROM winrate_bt_progress_count;
