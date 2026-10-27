-- Do the IEF / GLD / USO clusters line up with 5% up and 5% down days?
-- freq.gain_5pct_frequency supplies the ticker list. Daily moves are rebuilt
-- from market.backtest_start (close-to-close >= 5% or <= -5%), the same rule
-- as the 5% frequency study. GLD and USO are left out of the event set
-- because they are cluster features. IEF is not in that ticker list.
--
-- cluster_same_day is the regime on the move's own date (association).
-- cluster_prior is the regime on the previous cluster session (known at the
-- prior close), which is the prediction of today's move.
-- cluster = -1 is the baseline rate on the same rows. Lift is the cluster
-- rate divided by that baseline.

DROP TABLE IF EXISTS cluster_5pct_day;
CREATE TABLE cluster_5pct_day AS
WITH universe AS (
    SELECT symbol
    FROM freq.gain_5pct_frequency
    WHERE symbol NOT IN ('GLD', 'USO')
),
bars AS (
    SELECT b.symbol AS symbol,
           substr(b.Date, 1, 10) AS date,
           b.close / LAG(b.close) OVER (PARTITION BY b.symbol ORDER BY b.Date) - 1 AS ret
    FROM market.backtest_start b
    JOIN universe u ON u.symbol = b.symbol
    WHERE b.timeframe = '1d' AND length(b.Date) = 10 AND b.close > 0
),
labeled AS (
    SELECT date,
           cluster,
           LAG(cluster) OVER (ORDER BY date) AS prior_cluster
    FROM clusters.cluster_day
)
SELECT bars.symbol AS symbol,
       bars.date AS date,
       bars.ret AS ret,
       labeled.cluster AS cluster_same_day,
       labeled.prior_cluster AS cluster_prior,
       CASE WHEN bars.ret >= 0.05 THEN 1 ELSE 0 END AS gain_5,
       CASE WHEN bars.ret <= -0.05 THEN 1 ELSE 0 END AS drop_5
FROM bars
JOIN labeled ON labeled.date = bars.date
WHERE bars.ret IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_cluster_5pct_day ON cluster_5pct_day(symbol, date);

DROP TABLE IF EXISTS cluster_5pct_summary;
CREATE TABLE cluster_5pct_summary AS
WITH grouped AS (
    SELECT 'same_day' AS timing,
           cluster_same_day AS cluster,
           COUNT(*) AS symbol_days,
           COUNT(DISTINCT symbol) AS tickers,
           SUM(gain_5) AS gain_days,
           SUM(drop_5) AS drop_days
    FROM cluster_5pct_day
    GROUP BY cluster_same_day
    UNION ALL
    SELECT 'same_day', -1, COUNT(*), COUNT(DISTINCT symbol), SUM(gain_5), SUM(drop_5)
    FROM cluster_5pct_day
    UNION ALL
    SELECT 'next_day',
           cluster_prior,
           COUNT(*),
           COUNT(DISTINCT symbol),
           SUM(gain_5),
           SUM(drop_5)
    FROM cluster_5pct_day
    WHERE cluster_prior IS NOT NULL
    GROUP BY cluster_prior
    UNION ALL
    SELECT 'next_day', -1, COUNT(*), COUNT(DISTINCT symbol), SUM(gain_5), SUM(drop_5)
    FROM cluster_5pct_day
    WHERE cluster_prior IS NOT NULL
),
rates AS (
    SELECT timing, cluster, symbol_days, tickers, gain_days, drop_days,
           gain_days * 1.0 / symbol_days AS gain_rate,
           drop_days * 1.0 / symbol_days AS drop_rate
    FROM grouped
)
SELECT r.timing,
       r.cluster,
       r.symbol_days,
       r.tickers,
       r.gain_days,
       r.drop_days,
       r.gain_rate,
       r.drop_rate,
       CASE WHEN b.gain_rate > 0 THEN r.gain_rate / b.gain_rate END AS gain_lift,
       CASE WHEN b.drop_rate > 0 THEN r.drop_rate / b.drop_rate END AS drop_lift
FROM rates r
JOIN rates b ON b.timing = r.timing AND b.cluster = -1;
