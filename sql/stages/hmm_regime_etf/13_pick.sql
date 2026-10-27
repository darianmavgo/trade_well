-- The honest test of "the best ETF in a regime": pick the top ETFs on the in-sample years only,
-- then read what they did in the held-out year. avg_oos_rank_pct is their mean held-out rank as a
-- share of the ETFs ranked there: 50 is chance, near 0 is a repeat.
DROP TABLE IF EXISTS hmm_regime_pick;

CREATE TABLE hmm_regime_pick AS
SELECT
    d.regime AS regime,
    d.rnk AS rank_in,
    d.symbol AS symbol,
    d.n AS n_in,
    d.growth AS growth_in,
    o.n AS n_oos,
    o.growth AS growth_oos,
    o.rnk AS rank_oos,
    100.0 * o.rnk / o.ranked AS rank_oos_pct
FROM regime_rank d
LEFT JOIN regime_rank o ON o.symbol = d.symbol AND o.regime = d.regime AND o.sample = 'oos'
WHERE d.sample = 'in' AND d.rnk <= __TOP_N__
ORDER BY d.regime, d.rnk;
