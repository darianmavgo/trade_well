-- One row per ETF and sample: the evidence that the prior-close regime moves its next-session
-- return, and what holding it only when the regime was not stressed would have done.
--   mean_calm, mean_mid, mean_stress  mean next-session return by regime rank (0, 1, 2)
--   growth_calm, growth_mid, growth_stress  mean next-session log return (what compounds: it is the
--                                     mean return less half the variance, so volatility costs a leveraged ETF here)
--   t_stress                          Welch t of (not stressed minus stressed) mean return. Positive
--                                     means returns were better after a not-stressed close
--   t_growth                          the same Welch t on log returns
--   eta2                              share of return variance the regime explains
--   vol_ratio                         return volatility after a stressed close over after a calm close
--   compound_all, compound_filter     growth of 1 holding every session, and only sessions that
--                                     followed a not-stressed close
-- An ETF needs at least __MIN_STRESS__ sessions after a stressed close and as many after a
-- not-stressed one, or it has no row for that sample.
DROP TABLE IF EXISTS hmm_regime_etf;

CREATE TABLE hmm_regime_etf AS
SELECT
    t.symbol AS symbol,
    t.sample AS sample,
    t.n AS n,
    ss.n AS n_stress,
    ns.n AS n_not_stress,
    c.n AS n_calm,
    m.n AS n_mid,
    t.mean_ret AS mean_all,
    c.mean_ret AS mean_calm,
    m.mean_ret AS mean_mid,
    ss.mean_ret AS mean_stress,
    c.mean_ln AS growth_calm,
    m.mean_ln AS growth_mid,
    ss.mean_ln AS growth_stress,
    SQRT(t.mean_sq - t.mean_ret * t.mean_ret) AS vol_all,
    SQRT(c.mean_sq - c.mean_ret * c.mean_ret) AS vol_calm,
    SQRT(ss.mean_sq - ss.mean_ret * ss.mean_ret) AS vol_stress,
    SQRT(ss.mean_sq - ss.mean_ret * ss.mean_ret) / SQRT(c.mean_sq - c.mean_ret * c.mean_ret) AS vol_ratio,
    (ns.mean_ret - ss.mean_ret) / SQRT(
        (ns.mean_sq - ns.mean_ret * ns.mean_ret) * ns.n / (ns.n - 1) / ns.n
        + (ss.mean_sq - ss.mean_ret * ss.mean_ret) * ss.n / (ss.n - 1) / ss.n
    ) AS t_stress,
    (ns.mean_ln - ss.mean_ln) / SQRT(
        (ns.mean_ln_sq - ns.mean_ln * ns.mean_ln) * ns.n / (ns.n - 1) / ns.n
        + (ss.mean_ln_sq - ss.mean_ln * ss.mean_ln) * ss.n / (ss.n - 1) / ss.n
    ) AS t_growth,
    b.ss_between / (t.n * (t.mean_sq - t.mean_ret * t.mean_ret)) AS eta2,
    EXP(t.sum_ln) - 1 AS compound_all,
    EXP(ns.sum_ln) - 1 AS compound_filter,
    1.0 * ns.n / t.n AS share_invested
FROM total t
JOIN by_stress ss ON ss.symbol = t.symbol AND ss.sample = t.sample AND ss.is_stress = 1
JOIN by_stress ns ON ns.symbol = t.symbol AND ns.sample = t.sample AND ns.is_stress = 0
JOIN by_regime c ON c.symbol = t.symbol AND c.sample = t.sample AND c.regime = 0
JOIN by_regime m ON m.symbol = t.symbol AND m.sample = t.sample AND m.regime = 1
JOIN between_ss b ON b.symbol = t.symbol AND b.sample = t.sample
WHERE ss.n >= __MIN_STRESS__ AND ns.n >= __MIN_STRESS__ AND c.n >= 2 AND m.n >= 2;

DROP TABLE IF EXISTS hmm_regime_etf_info;

CREATE TABLE hmm_regime_etf_info AS
SELECT u.symbol AS symbol, u.name AS name, u.leverage AS leverage, u.direction AS direction, u.category AS category
FROM universe.universe u
WHERE u.is_etf = 1 AND u.active = 1;
