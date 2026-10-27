-- Where the fund's picks sit among other names, on momentum features. DESCRIPTIVE
-- ONLY: the universe is the 1,000 most liquid names *now* (plus every name the
-- fund traded), which is hindsight. Use 04 onward for any return figure.
-- Needs 01 and 'attach market_history.db as m'.
DROP TABLE IF EXISTS bars;
CREATE TABLE bars AS
SELECT symbol, substr(Date, 1, 10) AS date, open, high, low, close, volume
FROM m.backtest_start
WHERE length(Date) = 10 AND timeframe = '1d' AND Date >= '2024-06-01' AND symbol NOT LIKE '% %' AND close > 0;
CREATE INDEX bars_sd ON bars (symbol, date);

DROP TABLE IF EXISTS liq;
CREATE TABLE liq AS SELECT symbol, AVG(close * volume) AS dv FROM bars WHERE date >= '2026-07-01' GROUP BY symbol;
DROP TABLE IF EXISTS uni;
CREATE TABLE uni AS
SELECT symbol FROM liq WHERE dv >= (SELECT dv FROM liq ORDER BY dv DESC LIMIT 1 OFFSET 1000)
UNION SELECT DISTINCT symbol FROM tx WHERE symbol IN (SELECT symbol FROM liq);

DROP TABLE IF EXISTS px;
CREATE TABLE px AS
SELECT b.symbol, b.date, b.open, b.high, b.low, b.close, b.volume,
       b.close / LAG(b.close, 1)   OVER w - 1 AS r1,
       b.close / LAG(b.close, 5)   OVER w - 1 AS r5,
       b.close / LAG(b.close, 10)  OVER w - 1 AS r10,
       b.close / LAG(b.close, 21)  OVER w - 1 AS r21,
       b.close / LAG(b.close, 63)  OVER w - 1 AS r63,
       b.close / LAG(b.close, 126) OVER w - 1 AS r126,
       b.close / MAX(b.close) OVER (PARTITION BY b.symbol ORDER BY b.date ROWS 62 PRECEDING) - 1 AS dd63,
       b.close / AVG(b.close) OVER (PARTITION BY b.symbol ORDER BY b.date ROWS 49 PRECEDING) - 1 AS ma50,
       b.volume * 1.0 / AVG(b.volume) OVER (PARTITION BY b.symbol ORDER BY b.date ROWS 20 PRECEDING) AS vr20,
       b.open / LAG(b.close, 1) OVER w - 1 AS gap
FROM bars b WHERE b.symbol IN (SELECT symbol FROM uni)
WINDOW w AS (PARTITION BY b.symbol ORDER BY b.date);
DELETE FROM px WHERE date < '2025-10-06';

-- cross sectional percentile of every feature on every session
DROP TABLE IF EXISTS rk;
CREATE TABLE rk AS
SELECT symbol, date,
       PERCENT_RANK() OVER (PARTITION BY date ORDER BY r1)   AS p_r1,
       PERCENT_RANK() OVER (PARTITION BY date ORDER BY r5)   AS p_r5,
       PERCENT_RANK() OVER (PARTITION BY date ORDER BY r10)  AS p_r10,
       PERCENT_RANK() OVER (PARTITION BY date ORDER BY r21)  AS p_r21,
       PERCENT_RANK() OVER (PARTITION BY date ORDER BY r63)  AS p_r63,
       PERCENT_RANK() OVER (PARTITION BY date ORDER BY r126) AS p_r126,
       PERCENT_RANK() OVER (PARTITION BY date ORDER BY dd63) AS p_dd63,
       PERCENT_RANK() OVER (PARTITION BY date ORDER BY ma50) AS p_ma50,
       PERCENT_RANK() OVER (PARTITION BY date ORDER BY vr20) AS p_vr20,
       PERCENT_RANK() OVER (PARTITION BY date ORDER BY gap)  AS p_gap
FROM px WHERE r126 IS NOT NULL AND ABS(r1) < 0.5;
CREATE INDEX rk_sd ON rk (symbol, date);

.headers on
.print == average percentile of HELD names (0.5 is the median name) ==
SELECT COUNT(*) AS n, ROUND(AVG(p_r1), 2) AS r1, ROUND(AVG(p_r5), 2) AS r5, ROUND(AVG(p_r10), 2) AS r10,
       ROUND(AVG(p_r21), 2) AS r21, ROUND(AVG(p_r63), 2) AS r63, ROUND(AVG(p_r126), 2) AS r126,
       ROUND(AVG(p_dd63), 2) AS dd63, ROUND(AVG(p_ma50), 2) AS ma50, ROUND(AVG(p_vr20), 2) AS vr20, ROUND(AVG(p_gap), 2) AS gap
FROM rk JOIN held USING (symbol, date);
.print == NEW BUYS ==
SELECT COUNT(*) AS n, ROUND(AVG(p_r1), 2) AS r1, ROUND(AVG(p_r5), 2) AS r5, ROUND(AVG(p_r21), 2) AS r21,
       ROUND(AVG(p_r63), 2) AS r63, ROUND(AVG(p_dd63), 2) AS dd63, ROUND(AVG(p_ma50), 2) AS ma50
FROM rk JOIN newbuy USING (symbol, date);
.print == SELLS (what the name looked like on the day it was sold) ==
SELECT COUNT(*) AS n, ROUND(AVG(p_r1), 2) AS r1, ROUND(AVG(p_r5), 2) AS r5, ROUND(AVG(p_r21), 2) AS r21,
       ROUND(AVG(p_r63), 2) AS r63, ROUND(AVG(p_dd63), 2) AS dd63, ROUND(AVG(p_ma50), 2) AS ma50
FROM rk JOIN sold USING (symbol, date);
.print == share of held name-days in the top decile of each feature (0.10 is chance) ==
SELECT ROUND(AVG(p_r1 >= 0.9), 2) AS r1, ROUND(AVG(p_r5 >= 0.9), 2) AS r5, ROUND(AVG(p_r10 >= 0.9), 2) AS r10,
       ROUND(AVG(p_r21 >= 0.9), 2) AS r21, ROUND(AVG(p_r63 >= 0.9), 2) AS r63, ROUND(AVG(p_r126 >= 0.9), 2) AS r126,
       ROUND(AVG(p_dd63 >= 0.9), 2) AS dd63, ROUND(AVG(p_ma50 >= 0.9), 2) AS ma50, ROUND(AVG(p_vr20 >= 0.9), 2) AS vr20
FROM rk JOIN held USING (symbol, date);
