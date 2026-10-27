-- Daily GOOGL vs VOO inside market-context regimes.
-- market.backtest_start and clusters.cluster_day are attached by the study.
-- Returns are close-to-close simple returns. The lag uses the prior session
-- even when that session is outside cluster_day, then the join keeps only
-- labeled days. Beta = cov/var with divisor N. Alpha is the annualized
-- Jensen residual at a zero risk-free rate (252 * (mean GOOGL - beta * mean VOO)).
-- mean_residual_full_beta uses the full-sample beta, not the cluster beta.
-- cluster = -1 is the full joined sample.

DROP TABLE IF EXISTS googl_regime_day;
CREATE TABLE googl_regime_day AS
WITH googl AS (
    SELECT substr(Date, 1, 10) AS date,
           close / LAG(close) OVER (ORDER BY Date) - 1 AS ret
    FROM market.backtest_start
    WHERE symbol = 'GOOGL' AND timeframe = '1d' AND length(Date) = 10 AND close > 0
),
voo AS (
    SELECT substr(Date, 1, 10) AS date,
           close / LAG(close) OVER (ORDER BY Date) - 1 AS ret
    FROM market.backtest_start
    WHERE symbol = 'VOO' AND timeframe = '1d' AND length(Date) = 10 AND close > 0
)
SELECT c.date AS date,
       c.cluster AS cluster,
       g.ret AS googl_ret,
       v.ret AS voo_ret
FROM clusters.cluster_day c
JOIN googl g ON g.date = c.date
JOIN voo v ON v.date = c.date
WHERE g.ret IS NOT NULL AND v.ret IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_googl_regime_day_date ON googl_regime_day(date);

DROP TABLE IF EXISTS googl_regime_summary;
CREATE TABLE googl_regime_summary AS
WITH grouped AS (
    SELECT cluster AS cluster,
           COUNT(*) AS n,
           MIN(date) AS start_date,
           MAX(date) AS end_date,
           AVG(googl_ret) AS mean_googl,
           AVG(voo_ret) AS mean_voo,
           AVG(googl_ret * voo_ret) AS mean_prod,
           AVG(voo_ret * voo_ret) AS mean_voo_sq,
           SUM(LN(1 + googl_ret)) AS sum_ln_g,
           SUM(LN(1 + voo_ret)) AS sum_ln_v,
           SUM(CASE WHEN voo_ret < 0 THEN 1 ELSE 0 END) AS voo_down_days,
           AVG(CASE WHEN voo_ret < 0 THEN googl_ret END) AS mean_googl_when_voo_down,
           AVG(CASE WHEN voo_ret < 0 THEN CASE WHEN googl_ret < 0 THEN 1.0 ELSE 0.0 END END) AS share_googl_down_when_voo_down
    FROM googl_regime_day
    GROUP BY cluster
    UNION ALL
    SELECT -1,
           COUNT(*),
           MIN(date),
           MAX(date),
           AVG(googl_ret),
           AVG(voo_ret),
           AVG(googl_ret * voo_ret),
           AVG(voo_ret * voo_ret),
           SUM(LN(1 + googl_ret)),
           SUM(LN(1 + voo_ret)),
           SUM(CASE WHEN voo_ret < 0 THEN 1 ELSE 0 END),
           AVG(CASE WHEN voo_ret < 0 THEN googl_ret END),
           AVG(CASE WHEN voo_ret < 0 THEN CASE WHEN googl_ret < 0 THEN 1.0 ELSE 0.0 END END)
    FROM googl_regime_day
),
full AS (
    SELECT mean_googl, mean_voo, mean_prod, mean_voo_sq
    FROM grouped
    WHERE cluster = -1
),
calc AS (
    SELECT g.*,
           (f.mean_prod - f.mean_googl * f.mean_voo) / (f.mean_voo_sq - f.mean_voo * f.mean_voo) AS full_beta,
           (g.mean_prod - g.mean_googl * g.mean_voo) / (g.mean_voo_sq - g.mean_voo * g.mean_voo) AS beta
    FROM grouped g
    CROSS JOIN full f
)
SELECT cluster,
       n,
       start_date,
       end_date,
       mean_googl,
       mean_voo,
       EXP(sum_ln_g) - 1 AS googl_compound,
       EXP(sum_ln_v) - 1 AS voo_compound,
       beta,
       252.0 * (mean_googl - beta * mean_voo) AS alpha_ann,
       mean_googl - full_beta * mean_voo AS mean_residual_full_beta,
       full_beta,
       voo_down_days,
       mean_googl_when_voo_down,
       share_googl_down_when_voo_down
FROM calc;
