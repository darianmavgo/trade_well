-- The 13 decision-tree features, the next return and its 5 bucket class, for every symbol.
-- RSI14 and ATR14 are simple 14 bar averages. Bars need 200 bars of history first.
DROP VIEW IF EXISTS v_tree_features;
CREATE VIEW v_tree_features AS
SELECT
    symbol, date, close,
    (next_close - close) / close * 100.0 AS next_return,
    CASE
        WHEN next_close IS NULL THEN NULL
        WHEN (next_close - close) / close * 100.0 <= -5.0 THEN -2
        WHEN (next_close - close) / close * 100.0 < -1.0 THEN -1
        WHEN (next_close - close) / close * 100.0 <= 1.0 THEN 0
        WHEN (next_close - close) / close * 100.0 < 5.0 THEN 1
        ELSE 2
    END AS class,
    (close - prev_close)    / prev_close    * 100.0 AS return_1d,
    (close - prev_close_3)  / prev_close_3  * 100.0 AS return_3d,
    (close - prev_close_5)  / prev_close_5  * 100.0 AS return_5d,
    (close - prev_close_10) / prev_close_10 * 100.0 AS return_10d,
    CASE
        WHEN avg_loss14 > 0 THEN 100.0 - (100.0 / (1.0 + avg_gain14 / avg_loss14))
        WHEN avg_gain14 > 0 THEN 100.0
        ELSE 50.0
    END AS rsi14,
    (close - sma20)  / sma20  * 100.0 AS price_vs_sma20,
    (close - sma50)  / sma50  * 100.0 AS price_vs_sma50,
    (close - sma200) / sma200 * 100.0 AS price_vs_sma200,
    (sma20 - sma50)  / sma50  * 100.0 AS sma20_vs_50,
    CASE WHEN vol_avg20 > 0 THEN volume / vol_avg20 ELSE 1.0 END AS vol_ratio20,
    CASE WHEN atr14 > 0 THEN (high - low) / atr14 ELSE 1.0 END AS range_vs_atr14,
    CASE WHEN (high - low) > 0 THEN (close - low) / (high - low) ELSE 0.5 END AS close_near_high,
    MIN(down_streak, 10) AS consecutive_down
FROM v_tree_win
WHERE rn >= 201;
