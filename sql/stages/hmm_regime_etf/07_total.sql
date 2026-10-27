-- The same statistics over every session, regardless of regime.
DROP TABLE IF EXISTS total;

CREATE TABLE total AS
SELECT
    symbol,
    sample,
    COUNT(*) AS n,
    AVG(ret) AS mean_ret,
    AVG(ret * ret) AS mean_sq,
    SUM(LN(1 + ret)) AS sum_ln
FROM etf_sample
GROUP BY symbol, sample;
