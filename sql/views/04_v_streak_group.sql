-- Streak stage 2: up/down flags and the bar number. Every window in these views partitions by symbol
-- alone, so SQLite can push a symbol filter down to the index instead of scanning every bar.
DROP VIEW IF EXISTS v_streak_group;
CREATE VIEW v_streak_group AS
SELECT
    idx, symbol, date, open, high, low, close, volume, sma200,
    CASE WHEN close < prev_close THEN 1 ELSE 0 END AS is_down,
    CASE WHEN close > prev_close THEN 1 ELSE 0 END AS is_up,
    ROW_NUMBER() OVER (PARTITION BY symbol ORDER BY date) AS rn
FROM v_streak_diff;
