-- Tree feature stage 3: moving averages over 14, 20, 50 and 200 bars, and the length of the down run (bar number minus the last bar that was not down; bar 1 was left out, so it counts as not down).
DROP VIEW IF EXISTS v_tree_win;
CREATE VIEW v_tree_win AS
SELECT
    symbol, date, open, high, low, close, volume,
    prev_close, prev_close_3, prev_close_5, prev_close_10, next_close, rn,
    AVG(close) OVER w20  AS sma20,
    AVG(close) OVER w50  AS sma50,
    AVG(close) OVER w200 AS sma200,
    AVG(CASE WHEN chg > 0 THEN chg ELSE 0 END) OVER w14 AS avg_gain14,
    AVG(CASE WHEN chg < 0 THEN -chg ELSE 0 END) OVER w14 AS avg_loss14,
    AVG(volume) OVER w20 AS vol_avg20,
    AVG(true_range) OVER w14 AS atr14,
    CASE WHEN is_down = 1
         THEN rn - MAX(CASE WHEN is_down = 0 THEN rn ELSE 1 END) OVER (PARTITION BY symbol ORDER BY date)
         ELSE 0 END AS down_streak
FROM v_tree_chg
WINDOW
    w20  AS (PARTITION BY symbol ORDER BY date ROWS BETWEEN 19  PRECEDING AND CURRENT ROW),
    w50  AS (PARTITION BY symbol ORDER BY date ROWS BETWEEN 49  PRECEDING AND CURRENT ROW),
    w200 AS (PARTITION BY symbol ORDER BY date ROWS BETWEEN 199 PRECEDING AND CURRENT ROW),
    w14  AS (PARTITION BY symbol ORDER BY date ROWS BETWEEN 13  PRECEDING AND CURRENT ROW);
