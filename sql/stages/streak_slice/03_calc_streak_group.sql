-- A group id that only changes when the streak breaks. Bars that are not part
-- of a down (or up) run get group 0 so they are never counted into one.
INSERT INTO streak_group (symbol, date, close, sma50, sma200, is_down, is_up, down_grp, up_grp)
SELECT
    symbol, date, close, sma50, sma200, is_down, is_up,
    CASE WHEN is_down = 1
         THEN SUM(CASE WHEN is_down = 0 THEN 1 ELSE 0 END) OVER (PARTITION BY symbol ORDER BY date)
         ELSE 0 END,
    CASE WHEN is_up = 1
         THEN SUM(CASE WHEN is_up = 0 THEN 1 ELSE 0 END) OVER (PARTITION BY symbol ORDER BY date)
         ELSE 0 END
FROM streak_diff;
