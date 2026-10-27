-- Return statistics of each ETF in each prior-close regime.
DROP TABLE IF EXISTS by_regime;

CREATE TABLE by_regime AS
SELECT
    symbol,
    sample,
    r_prior AS regime,
    COUNT(*) AS n,
    AVG(ret) AS mean_ret,
    AVG(ret * ret) AS mean_sq,
    SUM(LN(1 + ret)) AS sum_ln,
    AVG(LN(1 + ret)) AS mean_ln,
    AVG(LN(1 + ret) * LN(1 + ret)) AS mean_ln_sq,
    AVG(CASE WHEN ret > 0 THEN 1.0 ELSE 0.0 END) AS win_rate
FROM etf_sample
GROUP BY symbol, sample, r_prior;
