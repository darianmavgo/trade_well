-- Streak stage 1 (view of sql/stages/streak_slice): each bar with its previous close and the 200-bar average.
DROP VIEW IF EXISTS v_streak_diff;
CREATE VIEW v_streak_diff AS
SELECT
    idx, symbol, date, open, high, low, close, volume,
    LAG(close, 1) OVER (PARTITION BY symbol ORDER BY date) AS prev_close,
    AVG(close) OVER (PARTITION BY symbol ORDER BY date ROWS BETWEEN 199 PRECEDING AND CURRENT ROW) AS sma200
FROM v_daily_bars;
