-- Change from the previous close, true range, and a group id for runs of down
-- closes. The group id is 0 on a bar that is not down, so such a bar never shares
-- a group with the down run that starts right after it.
INSERT INTO tf_chg (idx, symbol, date, open, high, low, close, volume, prev_close, prev_close_3, prev_close_5, prev_close_10, next_close, rn, chg, true_range, is_down, down_grp)
SELECT
    idx, symbol, date, open, high, low, close, volume, prev_close, prev_close_3, prev_close_5, prev_close_10, next_close, rn,
    close - prev_close,
    max(high - low, abs(high - prev_close), abs(low - prev_close)),
    CASE WHEN close < prev_close THEN 1 ELSE 0 END,
    CASE WHEN close < prev_close
         THEN SUM(CASE WHEN close < prev_close THEN 0 ELSE 1 END) OVER (PARTITION BY symbol ORDER BY date)
         ELSE 0 END
FROM tf_base
WHERE prev_close IS NOT NULL;
