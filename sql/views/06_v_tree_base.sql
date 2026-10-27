-- Tree feature stage 1 (view of sql/stages/tree_features): closes 1, 3, 5 and 10 bars back, and the next close.
DROP VIEW IF EXISTS v_tree_base;
CREATE VIEW v_tree_base AS
SELECT
    idx, symbol, date, open, high, low, close, volume,
    LAG(close, 1)  OVER w AS prev_close,
    LAG(close, 3)  OVER w AS prev_close_3,
    LAG(close, 5)  OVER w AS prev_close_5,
    LAG(close, 10) OVER w AS prev_close_10,
    LEAD(close, 1) OVER w AS next_close,
    ROW_NUMBER()   OVER w AS rn
FROM v_daily_bars
WINDOW w AS (PARTITION BY symbol ORDER BY date);
