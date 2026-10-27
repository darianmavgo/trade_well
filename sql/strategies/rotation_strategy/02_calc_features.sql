-- Momentum and liquidity features per symbol and session, from daily bars only.
-- Bars come from 260 calendar days before the window so the 126 session lookback
-- is full on the first session of the window. A session whose one day move is
-- over 50% is a split or a data error, not a return, and is dropped later.
INSERT INTO rot_feature (idx, symbol, date, open, high, low, close, volume, r1, r21, r63, r126, ma50, dd63, dv60, hist)
WITH daily AS (
    SELECT coalesce(idx, rowid, 0) AS idx, symbol, substr(Date, 1, 10) AS date,
           open, high, low, close, volume
    FROM backtest_start
    WHERE length(Date) = 10 AND close > 0 AND volume > 0
      AND (__USE_LIST__ = 0 OR symbol IN (__SYMBOLS__))
      AND substr(Date, 1, 10) >= date('__START_DATE__', '-260 day')
      AND substr(Date, 1, 10) <= '__END_DATE__'
)
SELECT
    idx, symbol, date, open, high, low, close, volume,
    close / LAG(close, 1)   OVER w - 1,
    close / LAG(close, 21)  OVER w - 1,
    close / LAG(close, 63)  OVER w - 1,
    close / LAG(close, 126) OVER w - 1,
    close / AVG(close) OVER (PARTITION BY symbol ORDER BY date ROWS 49 PRECEDING) - 1,
    close / MAX(close) OVER (PARTITION BY symbol ORDER BY date ROWS 62 PRECEDING) - 1,
    AVG(close * volume) OVER (PARTITION BY symbol ORDER BY date ROWS 59 PRECEDING),
    COUNT(*) OVER (PARTITION BY symbol ORDER BY date ROWS 126 PRECEDING)
FROM daily
WINDOW w AS (PARTITION BY symbol ORDER BY date);

CREATE INDEX rot_feature_sd ON rot_feature (symbol, date);
