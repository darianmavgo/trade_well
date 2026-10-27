-- Stage 5: the backtest engine replayed on the trades that closed live (live_vs_backtest_trade), so
-- live minus model separates fills from edge.
DROP TABLE IF EXISTS winrate_model;
CREATE TABLE winrate_model AS
SELECT strategy_id,
	SUM(CASE WHEN model_outcome = 'CLOSED' THEN 1 ELSE 0 END) AS model_n,
	SUM(CASE WHEN model_outcome = 'CLOSED' AND model_return_pct > 0 THEN 1 ELSE 0 END) AS model_wins
FROM live_vs_backtest_trade
WHERE live_outcome = 'CLOSED'
GROUP BY strategy_id;
