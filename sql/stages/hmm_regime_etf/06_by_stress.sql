-- The same statistics for stressed against not-stressed prior-close regimes.
DROP TABLE IF EXISTS by_stress;

CREATE TABLE by_stress AS
SELECT
    symbol,
    sample,
    is_stress,
    COUNT(*) AS n,
    AVG(ret) AS mean_ret,
    AVG(ret * ret) AS mean_sq,
    SUM(LN(1 + ret)) AS sum_ln,
    AVG(LN(1 + ret)) AS mean_ln,
    AVG(LN(1 + ret) * LN(1 + ret)) AS mean_ln_sq
FROM etf_sample
GROUP BY symbol, sample, is_stress;
