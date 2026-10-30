-- Adds one tuned hmm_strategy row (id suffix -g) per swept hmm strategy, with the hold, take profit and stop of its best in-sample gridsearch result.
-- Run: sqlite3 refdata/strategies.db "ATTACH 'data/reports/<N>/gridsearch.db' AS grid;" ".read sql/seed/hmm_tuned.sql"
-- The sweep stops before the held-out months, so the held-out backtest of the new rows is a fair test. gridsearch promote only writes streak rows, which is why this exists.
-- Only results with at least 30 trades and a win rate of at least 0.75 are used.
CREATE TEMP TABLE grid_ranked AS
SELECT strategy_id, hold_days, take_profit_pct, stop_loss_pct, win_rate, total_trades,
       ROW_NUMBER() OVER (PARTITION BY strategy_id ORDER BY win_rate DESC, total_trades DESC, id) AS rn
FROM grid.gridsearch_results
WHERE win_rate >= 0.75 AND total_trades >= 30 AND hold_days > 0;

CREATE TEMP TABLE grid_best AS
SELECT * FROM grid_ranked WHERE rn = 1;

INSERT OR IGNORE INTO hmm_strategy (id, name, hmm_symbol, trade_symbol, rule, stress_prob, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct, source_strategy, source_label, win_rate, total_trades)
SELECT h.id || '-g', h.name || ' tuned', h.hmm_symbol, h.trade_symbol, h.rule, h.stress_prob,
       b.hold_days, b.take_profit_pct, b.stop_loss_pct, h.allocation_pct, h.cash_yield, h.slippage_pct,
       h.id, 'hmm gridsearch', b.win_rate, b.total_trades
FROM grid_best b
JOIN hmm_strategy h ON h.id = b.strategy_id;

DROP TABLE grid_ranked;
DROP TABLE grid_best;
