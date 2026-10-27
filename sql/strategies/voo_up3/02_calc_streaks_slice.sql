-- Calculate VOO's consecutive up-close streak length per day, matching
-- VOOUpStreakDates() in pkg/strategy/voo_up3.go. down_grp/up_grp style
-- streak grouping. The grouping value must be zeroed on non-matching rows (a non-up day
-- must never share a group with the up-streak that starts right after it).
WITH diffs AS (
    SELECT
        substr(Date, 1, 10) AS date,
        close,
        LAG(close, 1) OVER (PARTITION BY symbol ORDER BY Date) AS prev_close
    FROM backtest_start
    WHERE symbol = 'VOO' AND length(Date) = 10
),
streaks AS (
    SELECT
        *,
        CASE WHEN close > prev_close THEN 1 ELSE 0 END AS is_up
    FROM diffs
    WHERE prev_close IS NOT NULL
),
streak_groups AS (
    SELECT
        *,
        CASE WHEN is_up = 1
             THEN SUM(CASE WHEN is_up = 0 THEN 1 ELSE 0 END) OVER (ORDER BY date)
             ELSE 0 END AS up_grp
    FROM streaks
)
INSERT INTO voo_up3_streaks_slice (date, up_streak)
SELECT
    date,
    CASE WHEN is_up = 1 THEN COUNT(*) OVER (PARTITION BY up_grp ORDER BY date) ELSE 0 END AS up_streak
FROM streak_groups;
