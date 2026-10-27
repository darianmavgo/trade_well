-- Stage 1: for each strategy, the newest run that has BOTH its in-sample and its out-of-sample
-- pass, so the two backtest lines on /live_vs_backtest_winrate come from one run and one set of settings.
DROP TABLE IF EXISTS temp.bs_run_both;
CREATE TABLE bs_run_both AS
SELECT strategy_id, run
FROM temp.bs_found
GROUP BY strategy_id, run
HAVING COUNT(DISTINCT pass) = 2;

DROP TABLE IF EXISTS temp.bs_best_run;
CREATE TABLE bs_best_run AS
SELECT strategy_id, MAX(run) AS run
FROM temp.bs_run_both
GROUP BY strategy_id;
