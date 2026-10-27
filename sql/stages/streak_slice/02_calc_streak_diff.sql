-- Up/down close flags for the watch symbol. The WHERE runs before the window,
-- so the first bar of the window has no previous close, exactly as when the
-- bars before __START_DATE__ are not loaded, and bars after __END_DATE__ are left out. The averages come from bar_sma,
-- which saw the full history.
INSERT INTO streak_diff (symbol, date, close, sma50, sma200, is_down, is_up)
SELECT
    b.symbol,
    substr(b.Date, 1, 10),
    b.close,
    s.sma50,
    s.sma200,
    CASE WHEN b.close < LAG(b.close) OVER (PARTITION BY b.symbol ORDER BY substr(b.Date, 1, 10)) THEN 1 ELSE 0 END,
    CASE WHEN b.close > LAG(b.close) OVER (PARTITION BY b.symbol ORDER BY substr(b.Date, 1, 10)) THEN 1 ELSE 0 END
FROM backtest_start b
JOIN bar_sma s ON s.symbol = b.symbol AND s.date = substr(b.Date, 1, 10)
WHERE b.symbol = '__SIGNAL_SYMBOL__' AND length(b.Date) = 10 AND substr(b.Date, 1, 10) >= '__START_DATE__' AND substr(b.Date, 1, 10) <= '__END_DATE__';
