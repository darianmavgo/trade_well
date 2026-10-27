-- The 200-bar average of close, taken over the symbol's whole history up to the
-- window end, so the first bars of the window already have a full average.
INSERT INTO par_sma (idx, symbol, date, open, high, low, close, volume, sma200)
SELECT
    coalesce(b.idx, b.rowid, 0), b.symbol, substr(b.Date, 1, 10),
    b.open, b.high, b.low, b.close, b.volume,
    AVG(b.close) OVER (PARTITION BY b.symbol ORDER BY substr(b.Date, 1, 10) ROWS BETWEEN 199 PRECEDING AND CURRENT ROW)
FROM backtest_start b
JOIN par_symbol p ON p.symbol = b.symbol
WHERE length(b.Date) = 10 AND substr(b.Date, 1, 10) <= '__END_DATE__';
