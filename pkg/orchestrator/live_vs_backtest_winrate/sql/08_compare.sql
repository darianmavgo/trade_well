-- Stage 8: the verdict per strategy. Needs 10 closed live trades before judging.
--   NO_BACKTEST  nothing to compare against      TOO_EARLY  fewer than 10 closed live trades
--   BELOW        live interval sits entirely under the backtest rate (live is really worse)
--   ABOVE        live interval sits entirely over it
--   IN_LINE      the backtest rate is inside the live interval (no evidence of a difference)
DROP TABLE IF EXISTS winrate_compare;
CREATE TABLE winrate_compare AS
SELECT *,
	live_win_rate - base_win_rate AS live_minus_base,
	live_win_rate - model_win_rate AS live_minus_model,
	CASE
		WHEN base_win_rate IS NULL THEN 'NO_BACKTEST'
		WHEN live_n < 10 THEN 'TOO_EARLY'
		WHEN live_ci_high < base_win_rate THEN 'BELOW'
		WHEN live_ci_low > base_win_rate THEN 'ABOVE'
		ELSE 'IN_LINE'
	END AS verdict
FROM winrate_interval;
