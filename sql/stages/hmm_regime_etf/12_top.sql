-- The best ETFs after a close in each regime, ranked on the whole period, with the same ETF in the
-- in-sample and held-out samples beside it. rank_oos of 1 of ranked_oos is a repeat. A rank in the
-- middle is regression to the mean.
DROP TABLE IF EXISTS hmm_regime_top;

CREATE TABLE hmm_regime_top AS
SELECT
    a.regime AS regime,
    a.rnk AS rank_all,
    a.ranked AS ranked_all,
    a.symbol AS symbol,
    i.name AS name,
    i.leverage AS leverage,
    i.category AS category,
    a.n AS n_all,
    a.mean_ret AS mean_all,
    a.growth AS growth_all,
    a.t_growth AS t_all,
    a.win_rate AS win_all,
    d.n AS n_in,
    d.growth AS growth_in,
    d.rnk AS rank_in,
    o.n AS n_oos,
    o.growth AS growth_oos,
    o.rnk AS rank_oos,
    o.ranked AS ranked_oos
FROM regime_rank a
JOIN hmm_regime_etf_info i ON i.symbol = a.symbol
LEFT JOIN regime_rank d ON d.symbol = a.symbol AND d.regime = a.regime AND d.sample = 'in'
LEFT JOIN regime_rank o ON o.symbol = a.symbol AND o.regime = a.regime AND o.sample = 'oos'
WHERE a.sample = 'all' AND a.rnk <= __TOP_N__
ORDER BY a.regime, a.rnk;
