-- Rank the active ETFs by growth within each sample and regime. The whole-period sample needs
-- __MIN_REGIME__ sessions in the regime (a short-lived ETF is not a ranking). The in-sample and
-- held-out samples only need __MIN_STRESS__, since they are read next to a ranked whole-period row.
DROP TABLE IF EXISTS regime_rank;

CREATE TABLE regime_rank AS
SELECT
    r.symbol AS symbol,
    r.sample AS sample,
    r.regime AS regime,
    r.n AS n,
    r.mean_ret AS mean_ret,
    r.growth AS growth,
    r.win_rate AS win_rate,
    r.t_growth AS t_growth,
    ROW_NUMBER() OVER (PARTITION BY r.sample, r.regime ORDER BY r.growth DESC, r.symbol) AS rnk,
    COUNT(*) OVER (PARTITION BY r.sample, r.regime) AS ranked
FROM regime_long r
JOIN hmm_regime_etf_info i ON i.symbol = r.symbol
WHERE r.n >= CASE WHEN r.sample = 'all' THEN __MIN_REGIME__ ELSE __MIN_STRESS__ END;
