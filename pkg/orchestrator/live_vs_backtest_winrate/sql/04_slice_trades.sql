-- Stage 4d: every closed live decision in the order it closed, for the running win rate.
DROP TABLE IF EXISTS winrate_trades;
CREATE TABLE winrate_trades AS
SELECT strategy_id, id, symbol, substr(exit_time, 1, 10) AS exit_date, win, pnl_pct
FROM winrate_decisions;
