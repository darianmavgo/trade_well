-- The 13 features, the next return and its 5 bucket class. RSI14 and ATR14 are
-- simple (unweighted) 14 bar averages, not Wilder smoothing. Bars need 200 bars of
-- history first.
INSERT INTO decision_tree_features_slice (
    date, close, next_return, class,
    return_1d, return_3d, return_5d, return_10d,
    rsi14, price_vs_sma20, price_vs_sma50, price_vs_sma200, sma20_vs_50,
    vol_ratio20, range_vs_atr14, close_near_high, consecutive_down
)
SELECT
    date,
    close,
    (next_close - close) / close * 100.0,
    CASE
        WHEN next_close IS NULL THEN NULL
        WHEN (next_close - close) / close * 100.0 <= -5.0 THEN -2
        WHEN (next_close - close) / close * 100.0 < -1.0 THEN -1
        WHEN (next_close - close) / close * 100.0 <= 1.0 THEN 0
        WHEN (next_close - close) / close * 100.0 < 5.0 THEN 1
        ELSE 2
    END,
    (close - prev_close) / prev_close * 100.0,
    (close - prev_close_3)  / prev_close_3  * 100.0,
    (close - prev_close_5)  / prev_close_5  * 100.0,
    (close - prev_close_10) / prev_close_10 * 100.0,
    CASE
        WHEN avg_loss14 > 0 THEN 100.0 - (100.0 / (1.0 + avg_gain14 / avg_loss14))
        WHEN avg_gain14 > 0 THEN 100.0
        ELSE 50.0
    END,
    (close - sma20)  / sma20  * 100.0,
    (close - sma50)  / sma50  * 100.0,
    (close - sma200) / sma200 * 100.0,
    (sma20 - sma50)  / sma50  * 100.0,
    CASE WHEN vol_avg20 > 0 THEN volume / vol_avg20 ELSE 1.0 END,
    CASE WHEN atr14 > 0 THEN (high - low) / atr14 ELSE 1.0 END,
    CASE WHEN (high - low) > 0 THEN (close - low) / (high - low) ELSE 0.5 END,
    MIN(down_streak, 10)
FROM tf_win
WHERE rn >= 201;
