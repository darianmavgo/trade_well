-- The observation: (return, realized volatility). Realized volatility is the sample standard
-- deviation of the last 20 returns, so it needs a full window (n = 20).
INSERT INTO hmm_obs (symbol, date, ret, vol)
SELECT
    symbol,
    date,
    ret,
    sqrt(MAX(mean_r2 - mean_r * mean_r, 0.0) * n / (n - 1.0))
FROM hmm_roll
WHERE n = 20
ORDER BY symbol, date;
