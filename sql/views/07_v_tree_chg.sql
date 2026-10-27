-- Tree feature stage 2: change from the previous close, true range, and whether the close is down.
-- The first bar of a symbol has no previous close and is left out, as in the stage.
DROP VIEW IF EXISTS v_tree_chg;
CREATE VIEW v_tree_chg AS
SELECT
    idx, symbol, date, open, high, low, close, volume,
    prev_close, prev_close_3, prev_close_5, prev_close_10, next_close, rn,
    close - prev_close AS chg,
    max(high - low, abs(high - prev_close), abs(low - prev_close)) AS true_range,
    CASE WHEN close < prev_close THEN 1 ELSE 0 END AS is_down
FROM v_tree_base
WHERE prev_close IS NOT NULL;
