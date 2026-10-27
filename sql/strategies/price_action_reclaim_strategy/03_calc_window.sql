-- Window bars only. Support is the lowest low from __SUPPORT_BARS__ bars back to __RECLAIM_GAP__
-- bars back, so the breakdown bar itself does not lower the level.
INSERT INTO par_window (idx, symbol, date, open, high, low, close, volume, sma, rn, prev_close, support)
SELECT
    idx, symbol, date, open, high, low, close, volume, sma,
    ROW_NUMBER() OVER w,
    LAG(close, 1) OVER w,
    MIN(low) OVER (ORDER BY date ROWS BETWEEN __SUPPORT_BARS__ PRECEDING AND __RECLAIM_GAP__ PRECEDING)
FROM par_sma
WHERE date >= '__START_DATE__'
WINDOW w AS (ORDER BY date);
