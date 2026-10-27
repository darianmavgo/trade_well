-- The trend average of close, taken over the symbol's whole history up to the window end, so
-- the first bars of the window already have a full average.
INSERT INTO par_sma (idx, symbol, date, open, high, low, close, volume, sma)
SELECT
    coalesce(idx, rowid, 0), symbol, substr(Date, 1, 10),
    open, high, low, close, volume,
    AVG(close) OVER (ORDER BY substr(Date, 1, 10) ROWS BETWEEN __SMA_PRECEDING__ PRECEDING AND CURRENT ROW)
FROM backtest_start
WHERE symbol = '__TRADE_SYMBOL__' AND length(Date) = 10 AND substr(Date, 1, 10) <= '__END_DATE__';
