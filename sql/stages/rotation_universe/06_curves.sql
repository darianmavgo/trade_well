-- Equal weight top K by the composite, rebalanced every close. The return of a session is the
-- next session close to close move, with no costs. bench is every ranked name.
DROP TABLE IF EXISTS bench;
CREATE TABLE bench AS SELECT date, AVG(f1) AS r FROM rk GROUP BY date;
DROP TABLE IF EXISTS p_top3;
CREATE TABLE p_top3 AS SELECT date, AVG(f1) AS r FROM rk WHERE rn <= 3 GROUP BY date;
DROP TABLE IF EXISTS p_top5;
CREATE TABLE p_top5 AS SELECT date, AVG(f1) AS r FROM rk WHERE rn <= 5 GROUP BY date;
DROP TABLE IF EXISTS p_top10;
CREATE TABLE p_top10 AS SELECT date, AVG(f1) AS r FROM rk WHERE rn <= 10 GROUP BY date;
DROP TABLE IF EXISTS p_top20;
CREATE TABLE p_top20 AS SELECT date, AVG(f1) AS r FROM rk WHERE rn <= 20 GROUP BY date;

-- Market gate: hold only while QQQ closes above its 200 session average, decided at the prior close.
DROP TABLE IF EXISTS reg;
CREATE TABLE reg AS
SELECT date, CASE WHEN close > AVG(close) OVER (ORDER BY date ROWS 199 PRECEDING) THEN 1 ELSE 0 END AS on_
FROM b0 WHERE symbol = 'QQQ';

DROP TABLE IF EXISTS gate;
CREATE TABLE gate AS
SELECT date, LAG(on_) OVER (ORDER BY date) AS lag_on FROM reg;

DROP TABLE IF EXISTS g_top10;
CREATE TABLE g_top10 AS
SELECT p.date, p.r * COALESCE(g.lag_on, 0) AS r FROM p_top10 p JOIN gate g USING (date);
DROP TABLE IF EXISTS g_top20;
CREATE TABLE g_top20 AS
SELECT p.date, p.r * COALESCE(g.lag_on, 0) AS r FROM p_top20 p JOIN gate g USING (date);

-- QQQ buy and hold over the same sessions.
DROP TABLE IF EXISTS q;
CREATE TABLE q AS SELECT date, close / LAG(close) OVER (ORDER BY date) - 1 AS r FROM b0 WHERE symbol = 'QQQ';
DELETE FROM q WHERE r IS NULL OR date < '2021-06-01';
