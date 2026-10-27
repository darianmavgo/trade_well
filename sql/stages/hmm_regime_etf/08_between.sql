-- Sum of squares of the regime means around the overall mean, weighted by sessions. Divided by
-- the total sum of squares it is eta squared: the share of next-session return variance the
-- prior-close regime explains.
DROP TABLE IF EXISTS between_ss;

CREATE TABLE between_ss AS
SELECT
    r.symbol AS symbol,
    r.sample AS sample,
    SUM(r.n * (r.mean_ret - t.mean_ret) * (r.mean_ret - t.mean_ret)) AS ss_between
FROM by_regime r
JOIN total t ON t.symbol = r.symbol AND t.sample = r.sample
GROUP BY r.symbol, r.sample;
