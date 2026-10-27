-- Where each strategy's ongoing win rate stands now: its newest row of trade_history_running.
DROP VIEW IF EXISTS trade_history_win_rate;
CREATE VIEW trade_history_win_rate AS
SELECT strategy_id, n AS trades, wins, win_rate, ci_low, ci_high, first_close, exit_date AS last_close
FROM trade_history_running
WHERE is_latest = 1;
