DROP TABLE IF EXISTS check_overfit;
CREATE TABLE check_overfit AS
SELECT
	s.strategy_id,
	s.folds,
	s.is_sharpe,
	s.oos_sharpe,
	s.is_return_pct,
	s.oos_return_pct,
	s.oos_trades,
	s.oos_positive_folds,
	s.trials,
	COALESCE(s.oos_idle_days, 0) AS oos_idle_days,
	COALESCE(s.oos_days, 0) AS oos_days,
	CASE
		WHEN s.oos_trades < g.min_oos_trades THEN 'INSUFFICIENT'
		WHEN s.trials >= g.trial_cutoff AND s.is_sharpe > 0.5 AND s.oos_sharpe < s.is_sharpe * g.decay THEN 'CURVE_FIT'
		WHEN s.is_sharpe > 0 AND s.oos_sharpe < s.is_sharpe * g.decay THEN 'DECAYS'
		WHEN s.oos_sharpe > 0 AND (s.is_sharpe <= 0 OR s.oos_sharpe >= s.is_sharpe * 0.5) THEN 'HOLDS'
		ELSE 'DECAYS'
	END AS verdict
FROM walk_forward_summary s
JOIN check_overfit_gate g;
