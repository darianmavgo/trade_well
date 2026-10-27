-- Stage 3: the result file to keep for each strategy and pass. note is same_run when both
-- passes come from one run, split_runs when they do not (the page's lines then mix runs).
DROP TABLE IF EXISTS temp.bs_pick;
CREATE TABLE bs_pick AS
SELECT f.strategy_id, f.pass, f.run, f.path, f.rel, f.trades, f.win_rate, f.window_start, f.window_end,
	CASE WHEN b.run IS NOT NULL THEN 'same_run' ELSE 'split_runs' END AS note
FROM temp.bs_found f
JOIN temp.bs_newest n ON n.strategy_id = f.strategy_id AND n.pass = f.pass
LEFT JOIN temp.bs_best_run b ON b.strategy_id = f.strategy_id
WHERE f.run = COALESCE(b.run, n.run);
