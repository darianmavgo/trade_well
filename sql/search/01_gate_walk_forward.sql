-- Stage 1: hard OOS gate over walk_forward_fold (database: walk_forward.db).
-- One row per strategy that survives. Thresholds are deliberately strict
-- because thousands of strategies are screened on the same data.
-- Needs `.parameter set :max_sleeve_dd 0.10` in the sqlite3 session (README.md).
DROP TABLE IF EXISTS search_01_gate;
CREATE TABLE search_01_gate AS
SELECT strategy_id,
       COUNT(*)                                   AS n_folds,
       SUM(oos_trades)                            AS sum_oos_trades,
       AVG(oos_sharpe)                            AS mean_oos_sharpe,
       AVG(is_sharpe)                             AS mean_is_sharpe,
       AVG(oos_return_pct)                        AS mean_oos_ret,
       MAX(oos_max_dd)                            AS worst_fold_dd,
       SUM(oos_return_pct > 0) * 1.0 / COUNT(*)   AS pos_fold_share
FROM walk_forward_fold
GROUP BY strategy_id
HAVING COUNT(*) >= 4
   AND SUM(oos_trades) >= 30
   AND AVG(oos_sharpe) >= 1.5
   AND SUM(oos_return_pct > 0) * 1.0 / COUNT(*) >= 0.75
   AND AVG(is_sharpe) >= 0.75
   AND MAX(oos_max_dd) <= :max_sleeve_dd;
