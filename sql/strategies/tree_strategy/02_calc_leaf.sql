-- Walk every bar down the saved tree. A bar starts at the root (path empty) and
-- turns left when the node's feature is at or below its threshold, otherwise
-- right (a missing value turns right), until it reaches a leaf. The leaf's pred
-- is the 5 bucket class the tree predicts for the next bar.
INSERT INTO tree_leaf (date, pred)
WITH RECURSIVE walk(date, path) AS (
    SELECT date, '' FROM decision_tree_features_slice
    UNION ALL
    SELECT
        w.date,
        w.path || CASE WHEN
            CASE n.feature
                WHEN 'Return1d' THEN f.return_1d
                WHEN 'Return3d' THEN f.return_3d
                WHEN 'Return5d' THEN f.return_5d
                WHEN 'Return10d' THEN f.return_10d
                WHEN 'RSI14' THEN f.rsi14
                WHEN 'PriceVsSMA20' THEN f.price_vs_sma20
                WHEN 'PriceVsSMA50' THEN f.price_vs_sma50
                WHEN 'PriceVsSMA200' THEN f.price_vs_sma200
                WHEN 'SMA20Vs50' THEN f.sma20_vs_50
                WHEN 'VolRatio20' THEN f.vol_ratio20
                WHEN 'RangeVsATR14' THEN f.range_vs_atr14
                WHEN 'CloseNearHigh' THEN f.close_near_high
                WHEN 'ConsecutiveDown' THEN f.consecutive_down
            END <= n.threshold THEN 'L' ELSE 'R' END
    FROM walk w
    JOIN treemodel.tree_node n ON n.symbol = '__SYMBOL__' AND n.path = w.path AND n.feature IS NOT NULL
    JOIN decision_tree_features_slice f ON f.date = w.date
)
SELECT w.date, n.pred
FROM walk w
JOIN treemodel.tree_node n ON n.symbol = '__SYMBOL__' AND n.path = w.path AND n.feature IS NULL;
