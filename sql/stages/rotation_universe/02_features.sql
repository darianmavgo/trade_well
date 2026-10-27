-- The features of each symbol and session: returns over 1, 21, 63 and 126 sessions, the next
-- 1 and 5 session returns (f1 and f5, for the curves only), the drawdown from the 63 session high,
-- the distance above the 50 session average and the 60 session average dollar volume (dv60).
-- A symbol needs 127 sessions of history before its first row.
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
