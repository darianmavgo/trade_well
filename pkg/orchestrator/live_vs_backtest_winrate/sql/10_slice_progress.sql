-- Stage 10: the running live win rate after each close, with its 95% Wilson interval
-- (z = 1.96), so the chart shows the range narrowing as trades accumulate.
DROP TABLE IF EXISTS winrate_progress;
CREATE TABLE winrate_progress AS
SELECT strategy_id, n, symbol, exit_date, win, pnl_pct, wins, 1.0 * wins / n AS rate,
	((1.0 * wins / n) + 1.96 * 1.96 / (2.0 * n)
	 - 1.96 * sqrt((1.0 * wins / n) * (1 - 1.0 * wins / n) / n + 1.96 * 1.96 / (4.0 * n * n)))
	/ (1 + 1.96 * 1.96 / n) AS ci_low,
	((1.0 * wins / n) + 1.96 * 1.96 / (2.0 * n)
	 + 1.96 * sqrt((1.0 * wins / n) * (1 - 1.0 * wins / n) / n + 1.96 * 1.96 / (4.0 * n * n)))
	/ (1 + 1.96 * 1.96 / n) AS ci_high
FROM winrate_progress_count;
