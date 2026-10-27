-- The ongoing win rate: after each closed decision, a strategy's win rate so far with its 95%
-- Wilson interval (z = 1.96), which stays honest at the handful of trades a live strategy has.
-- It narrows as trades are attributed to the strategy.
DROP VIEW IF EXISTS trade_history_running;
CREATE VIEW trade_history_running AS
SELECT strategy_id, n, symbol, exit_date, win, pnl_pct, wins, first_close, is_latest,
	1.0 * wins / n AS win_rate,
	((1.0 * wins / n) + 1.96 * 1.96 / (2.0 * n)
	 - 1.96 * sqrt((1.0 * wins / n) * (1 - 1.0 * wins / n) / n + 1.96 * 1.96 / (4.0 * n * n)))
	/ (1 + 1.96 * 1.96 / n) AS ci_low,
	((1.0 * wins / n) + 1.96 * 1.96 / (2.0 * n)
	 + 1.96 * sqrt((1.0 * wins / n) * (1 - 1.0 * wins / n) / n + 1.96 * 1.96 / (4.0 * n * n)))
	/ (1 + 1.96 * 1.96 / n) AS ci_high
FROM trade_history_running_count;
