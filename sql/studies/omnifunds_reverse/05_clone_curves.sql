-- Equal weight top-K by a momentum composite, rebalanced every close, on the
-- point in time universe of 04. Return of a session is the next session's
-- close to close move, with no costs. The composite is the mean of five
-- percentile ranks: 21, 63 and 126 session return, distance above the 50 session
-- average, distance from the 63 session high. A session move over 50% is a split
-- or bad print and is dropped, as in sql/strategies/rotation_strategy.
-- Needs the tables of 04. The first session is 2021-06-01 (126 sessions of lookback).
DROP TABLE IF EXISTS u;
CREATE TABLE u AS
SELECT f.* FROM f JOIN lq USING (symbol, date)
WHERE ABS(r1) < 0.5 AND r126 IS NOT NULL AND date >= '2021-06-01';
CREATE INDEX u_d ON u (date);

DROP TABLE IF EXISTS sc;
CREATE TABLE sc AS
SELECT symbol, date, f1, f5, r1,
       (PERCENT_RANK() OVER (PARTITION BY date ORDER BY r21) + PERCENT_RANK() OVER (PARTITION BY date ORDER BY r63)
      + PERCENT_RANK() OVER (PARTITION BY date ORDER BY r126) + PERCENT_RANK() OVER (PARTITION BY date ORDER BY ma50)
      + PERCENT_RANK() OVER (PARTITION BY date ORDER BY dd63)) / 5.0 AS s
FROM u;

DROP TABLE IF EXISTS rk;
CREATE TABLE rk AS
SELECT symbol, date, f1, f5, s, ROW_NUMBER() OVER (PARTITION BY date ORDER BY s DESC) AS rn
FROM sc WHERE f1 IS NOT NULL AND ABS(f1) < 0.5;
CREATE INDEX rk_d ON rk (date, rn);

DROP TABLE IF EXISTS bench;   CREATE TABLE bench   AS SELECT date, AVG(f1) AS r FROM rk GROUP BY date;
DROP TABLE IF EXISTS p_top3;  CREATE TABLE p_top3  AS SELECT date, AVG(f1) AS r FROM rk WHERE rn <= 3  GROUP BY date;
DROP TABLE IF EXISTS p_top5;  CREATE TABLE p_top5  AS SELECT date, AVG(f1) AS r FROM rk WHERE rn <= 5  GROUP BY date;
DROP TABLE IF EXISTS p_top10; CREATE TABLE p_top10 AS SELECT date, AVG(f1) AS r FROM rk WHERE rn <= 10 GROUP BY date;
DROP TABLE IF EXISTS p_top20; CREATE TABLE p_top20 AS SELECT date, AVG(f1) AS r FROM rk WHERE rn <= 20 GROUP BY date;

-- market gate: hold only while QQQ closes above its 200 session average (decided at the prior close)
DROP TABLE IF EXISTS reg;
CREATE TABLE reg AS
SELECT date, CASE WHEN close > AVG(close) OVER (ORDER BY date ROWS 199 PRECEDING) THEN 1 ELSE 0 END AS on_
FROM b0 WHERE symbol = 'QQQ';
DROP TABLE IF EXISTS g_top10; CREATE TABLE g_top10 AS
SELECT p.date, p.r * COALESCE(g.lag_on, 0) AS r FROM p_top10 p JOIN (SELECT date, LAG(on_) OVER (ORDER BY date) AS lag_on FROM reg) g USING (date);
DROP TABLE IF EXISTS g_top20; CREATE TABLE g_top20 AS
SELECT p.date, p.r * COALESCE(g.lag_on, 0) AS r FROM p_top20 p JOIN (SELECT date, LAG(on_) OVER (ORDER BY date) AS lag_on FROM reg) g USING (date);

-- QQQ buy and hold over the same sessions
DROP TABLE IF EXISTS q;
CREATE TABLE q AS SELECT date, close / LAG(close) OVER (ORDER BY date) - 1 AS r FROM b0 WHERE symbol = 'QQQ';
DELETE FROM q WHERE r IS NULL OR date < '2021-06-01';

-- the same curves restricted to the fund's own window (2025-10-07 on)
DROP TABLE IF EXISTS w_bench; CREATE TABLE w_bench AS SELECT * FROM bench   WHERE date >= '2025-10-07';
DROP TABLE IF EXISTS w_top10; CREATE TABLE w_top10 AS SELECT * FROM p_top10 WHERE date >= '2025-10-07';
DROP TABLE IF EXISTS w_top20; CREATE TABLE w_top20 AS SELECT * FROM p_top20 WHERE date >= '2025-10-07';

-- the fund itself, from its time weighted daily return (needs 'attach ... as i')
DROP TABLE IF EXISTS fundret;
CREATE TABLE fundret AS SELECT date, daily_return AS r FROM i.ibkr_return_daily WHERE daily_return IS NOT NULL AND date >= '2025-10-07';

-- calendar year return of each curve
.headers on
SELECT 'top10' AS curve, substr(date, 1, 4) AS yr, COUNT(*) AS n, ROUND((EXP(SUM(LN(1 + r))) - 1) * 100, 1) AS total_pct FROM p_top10 GROUP BY 2;
SELECT 'top20' AS curve, substr(date, 1, 4) AS yr, COUNT(*) AS n, ROUND((EXP(SUM(LN(1 + r))) - 1) * 100, 1) AS total_pct FROM p_top20 GROUP BY 2;
