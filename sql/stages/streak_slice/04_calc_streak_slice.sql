-- Consecutive down and up closes ending on each bar.
INSERT INTO streak_slice (symbol, date, close, sma50, sma200, down_streak, up_streak)
SELECT
    symbol, date, close, sma50, sma200,
    CASE WHEN is_down = 1 THEN COUNT(*) OVER (PARTITION BY symbol, down_grp ORDER BY date) ELSE 0 END,
    CASE WHEN is_up = 1 THEN COUNT(*) OVER (PARTITION BY symbol, up_grp ORDER BY date) ELSE 0 END
FROM streak_group;
