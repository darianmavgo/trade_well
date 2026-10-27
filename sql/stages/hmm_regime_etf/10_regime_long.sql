-- One row per ETF, sample and regime (0 calm, 1 normal, 2 stressed) with what an ETF earned the
-- session after a close in that regime. growth is the mean log return, what compounds.
-- t_growth is the t statistic of that mean against zero: a "top" ETF with a low t is luck.
DROP TABLE IF EXISTS regime_long;

CREATE TABLE regime_long AS
SELECT
    symbol,
    sample,
    regime,
    n,
    mean_ret,
    mean_ln AS growth,
    win_rate,
    mean_ln / SQRT((mean_ln_sq - mean_ln * mean_ln) * n / (n - 1) / n) AS t_growth
FROM by_regime
WHERE n >= 2;
