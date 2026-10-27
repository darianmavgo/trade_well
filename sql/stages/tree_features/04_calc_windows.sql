-- Moving averages over 14, 20, 50 and 200 bars, and the length of the down run.
INSERT INTO tf_win (date, open, high, low, close, volume, prev_close, prev_close_3, prev_close_5, prev_close_10, next_close, rn, sma20, sma50, sma200, avg_gain14, avg_loss14, vol_avg20, atr14, down_streak)
SELECT
    date, open, high, low, close, volume, prev_close, prev_close_3, prev_close_5, prev_close_10, next_close, rn,
    AVG(close) OVER w20,
    AVG(close) OVER w50,
    AVG(close) OVER w200,
    AVG(CASE WHEN chg > 0 THEN chg ELSE 0 END) OVER w14,
    AVG(CASE WHEN chg < 0 THEN -chg ELSE 0 END) OVER w14,
    AVG(volume) OVER w20,
    AVG(true_range) OVER w14,
    CASE WHEN is_down = 1 THEN COUNT(*) OVER (PARTITION BY symbol, down_grp ORDER BY date) ELSE 0 END
FROM tf_chg
WINDOW
    w20  AS (PARTITION BY symbol ORDER BY date ROWS BETWEEN 19  PRECEDING AND CURRENT ROW),
    w50  AS (PARTITION BY symbol ORDER BY date ROWS BETWEEN 49  PRECEDING AND CURRENT ROW),
    w200 AS (PARTITION BY symbol ORDER BY date ROWS BETWEEN 199 PRECEDING AND CURRENT ROW),
    w14  AS (PARTITION BY symbol ORDER BY date ROWS BETWEEN 13  PRECEDING AND CURRENT ROW);
