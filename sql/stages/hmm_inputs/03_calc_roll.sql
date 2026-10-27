-- Count, mean and mean square of the 20 most recent returns, today included.
INSERT INTO hmm_roll (symbol, date, ret, n, mean_r, mean_r2)
SELECT
    symbol,
    date,
    ret,
    COUNT(ret) OVER w,
    AVG(ret) OVER w,
    AVG(ret * ret) OVER w
FROM hmm_ret
WHERE ret IS NOT NULL
WINDOW w AS (PARTITION BY symbol ORDER BY date ROWS BETWEEN 19 PRECEDING AND CURRENT ROW);
