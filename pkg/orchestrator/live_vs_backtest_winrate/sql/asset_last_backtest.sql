-- Run on a refdata strategies.db ASSET: each strategy's most recent whole-window backtest. It has
-- no in-sample / out-of-sample split. Only these columns are loaded into Turso.
SELECT strategy_id, COALESCE(total_trades, 0), win_rate, created_at
FROM strategy_last_backtest
