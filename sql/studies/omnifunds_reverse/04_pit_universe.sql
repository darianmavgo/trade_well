-- Point in time universe: each session, the 1,000 most liquid names by 60 session
-- average dollar volume known at that close. No name is chosen with hindsight.
-- Needs 'attach market_history.db as m'. This is the slow step (minutes).
DROP TABLE IF EXISTS b0;
CREATE TABLE b0 AS
SELECT symbol, substr(Date, 1, 10) AS date, open, high, low, close, volume
FROM m.backtest_start
WHERE length(Date) = 10 AND timeframe = '1d' AND close > 0 AND volume > 0
  AND symbol NOT LIKE '% %' AND symbol NOT GLOB '*[0-9]*';   -- no options, no numbered series
CREATE INDEX b0_sd ON b0 (symbol, date);

DROP TABLE IF EXISTS f;
CREATE TABLE f AS
SELECT symbol, date, close,
       close / LAG(close, 1) OVER w - 1   AS r1,
       LEAD(close, 1) OVER w / close - 1  AS f1,
       LEAD(close, 5) OVER w / close - 1  AS f5,
       close / LAG(close, 21) OVER w - 1  AS r21,
       close / LAG(close, 63) OVER w - 1  AS r63,
       close / LAG(close, 126) OVER w - 1 AS r126,
       close / MAX(close) OVER (PARTITION BY symbol ORDER BY date ROWS 62 PRECEDING) - 1 AS dd63,
       close / AVG(close) OVER (PARTITION BY symbol ORDER BY date ROWS 49 PRECEDING) - 1 AS ma50,
       AVG(close * volume) OVER (PARTITION BY symbol ORDER BY date ROWS 59 PRECEDING) AS dv60,
       COUNT(*) OVER (PARTITION BY symbol ORDER BY date ROWS 126 PRECEDING) AS hist
FROM b0
WINDOW w AS (PARTITION BY symbol ORDER BY date);
DELETE FROM f WHERE hist < 127 OR dv60 IS NULL;
CREATE INDEX f_sd ON f (symbol, date);
CREATE INDEX f_dd ON f (date, dv60);

DROP TABLE IF EXISTS lq;
CREATE TABLE lq AS
SELECT symbol, date FROM (
    SELECT symbol, date, ROW_NUMBER() OVER (PARTITION BY date ORDER BY dv60 DESC) AS lr FROM f
) WHERE lr <= 1000;
CREATE INDEX lq_sd ON lq (symbol, date);
