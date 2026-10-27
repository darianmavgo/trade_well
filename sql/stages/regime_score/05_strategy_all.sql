INSERT INTO strategy_all (strategy_id, win, n, avg_return_pct)
SELECT strategy_id, win, COUNT(*), AVG(return_pct)
FROM trade_regime
GROUP BY strategy_id, win
