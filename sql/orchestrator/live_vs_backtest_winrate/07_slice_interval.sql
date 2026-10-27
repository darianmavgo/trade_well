-- Stage 7: 95% Wilson interval for the live win rate (z = 1.96); it stays honest at the
-- 5 to 20 trades a live strategy has. The baseline is the out-of-sample rate, else the
-- in-sample rate, else the whole-window backtest rate.
DROP TABLE IF EXISTS winrate_interval;
CREATE TABLE winrate_interval AS
SELECT *,
	COALESCE(oos_win_rate, is_win_rate, all_win_rate) AS base_win_rate,
	CASE WHEN oos_win_rate IS NOT NULL THEN 'oos' WHEN is_win_rate IS NOT NULL THEN 'is'
		WHEN all_win_rate IS NOT NULL THEN 'all' END AS base_kind,
	CASE WHEN live_n > 0 THEN
		(live_win_rate + 1.96 * 1.96 / (2.0 * live_n)
		 - 1.96 * sqrt(live_win_rate * (1 - live_win_rate) / live_n + 1.96 * 1.96 / (4.0 * live_n * live_n)))
		/ (1 + 1.96 * 1.96 / live_n) END AS live_ci_low,
	CASE WHEN live_n > 0 THEN
		(live_win_rate + 1.96 * 1.96 / (2.0 * live_n)
		 + 1.96 * sqrt(live_win_rate * (1 - live_win_rate) / live_n + 1.96 * 1.96 / (4.0 * live_n * live_n)))
		/ (1 + 1.96 * 1.96 / live_n) END AS live_ci_high
FROM winrate_joined;
