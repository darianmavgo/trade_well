-- Prices with the closes 1, 3, 5 and 10 bars back, and the next close.
INSERT INTO tf_base (idx, symbol, date, open, high, low, close, volume, prev_close, prev_close_3, prev_close_5, prev_close_10, next_close, rn)
SELECT
    coalesce(idx, rowid, 0), symbol, substr(Date, 1, 10),
    open, high, low, close, volume,
    LAG(close, 1)  OVER w,
    LAG(close, 3)  OVER w,
    LAG(close, 5)  OVER w,
    LAG(close, 10) OVER w,
    LEAD(close, 1) OVER w,
    ROW_NUMBER()   OVER w
FROM backtest_start
WHERE symbol = '__SYMBOL__' AND length(Date) = 10
WINDOW w AS (PARTITION BY symbol ORDER BY Date);
