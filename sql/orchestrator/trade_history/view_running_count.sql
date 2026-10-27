-- Every closed decision in the order it closed, with how many a strategy had closed by then (n)
-- and how many of those were wins. A strategy's row with is_latest = 1 is its newest.
DROP VIEW IF EXISTS trade_history_running_count;
CREATE VIEW trade_history_running_count AS
SELECT strategy_id, symbol, substr(exit_time, 1, 10) AS exit_date, win, pnl_pct, id,
	ROW_NUMBER() OVER w AS n,
	SUM(win) OVER w AS wins,
	MIN(substr(exit_time, 1, 10)) OVER (PARTITION BY strategy_id) AS first_close,
	ROW_NUMBER() OVER (PARTITION BY strategy_id ORDER BY exit_time DESC, id DESC) = 1 AS is_latest
FROM trade_history_decision
WINDOW w AS (PARTITION BY strategy_id ORDER BY exit_time, id ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW);
