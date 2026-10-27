-- Window bars only. Support is the lowest low from 25 bars back to 5 bars back,
-- so the breakdown bar itself does not lower the level.
INSERT INTO par_window (idx, symbol, date, open, high, low, close, volume, sma200, rn, prev_close, support)
SELECT
    idx, symbol, date, open, high, low, close, volume, sma200,
    ROW_NUMBER() OVER w,
    LAG(close, 1) OVER w,
    MIN(low) OVER (PARTITION BY symbol ORDER BY date ROWS BETWEEN 25 PRECEDING AND 5 PRECEDING)
FROM par_sma
WHERE date >= '__START_DATE__'
WINDOW w AS (PARTITION BY symbol ORDER BY date);
