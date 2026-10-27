-- CAR, max drawdown and Calmar of a daily return series. Replace @T@ with a table
-- (date, r). CAR is annualised on 252 sessions, drawdown is the largest fall of the
-- compounded curve from its running peak. Example:
--   sd '@T@' p_top10 stats.sql | sqlite3 scratch.db
WITH s AS (SELECT date, r, EXP(SUM(LN(1 + r)) OVER (ORDER BY date)) AS eq FROM @T@),
d AS (SELECT *, MAX(eq) OVER (ORDER BY date) AS pk FROM s),
x AS (SELECT COUNT(*) AS n, EXP(SUM(LN(1 + r))) AS tot, MAX(1 - eq / pk) AS mdd FROM d)
SELECT '@T@' AS name, ROUND((tot - 1) * 100, 1) AS total_pct,
       ROUND((EXP(LN(tot) * 252.0 / n) - 1) * 100, 1) AS car_pct,
       ROUND(mdd * 100, 1) AS mdd_pct,
       ROUND((EXP(LN(tot) * 252.0 / n) - 1) / mdd, 2) AS calmar
FROM x;
