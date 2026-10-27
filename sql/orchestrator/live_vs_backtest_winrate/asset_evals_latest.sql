-- Run on the strategy_evals ASSET (backtestgosqlite): the newest evaluation of each strategy, which
-- becomes its in-sample and out-of-sample win rate. Only these columns are loaded into Turso.
SELECT e.strategy_id, e.is_start, e.is_end, e.oos_start, e.oos_end,
	COALESCE(e.is_trades, 0), e.is_win_rate, COALESCE(e.oos_trades, 0), e.oos_win_rate, e.created_at
FROM strategy_evals e
JOIN (SELECT strategy_id, MAX(id) AS eval_id FROM strategy_evals GROUP BY strategy_id) l ON l.eval_id = e.id
