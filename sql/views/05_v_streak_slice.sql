-- Streak stage 3: consecutive down and up closes of every symbol (0 when the bar is not part of a run).
-- A run's length is the bar number minus the number of the last bar that was not in the run.
DROP VIEW IF EXISTS v_streak_slice;
CREATE VIEW v_streak_slice AS
SELECT
    idx, symbol, date, open, high, low, close, volume, sma200,
    CASE WHEN is_down = 1
         THEN rn - MAX(CASE WHEN is_down = 0 THEN rn ELSE 0 END) OVER (PARTITION BY symbol ORDER BY date)
         ELSE 0 END AS down_streak,
    CASE WHEN is_up = 1
         THEN rn - MAX(CASE WHEN is_up = 0 THEN rn ELSE 0 END) OVER (PARTITION BY symbol ORDER BY date)
         ELSE 0 END AS up_streak
FROM v_streak_group;
