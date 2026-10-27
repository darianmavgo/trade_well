-- Stage 12: the compiled-in trades fill in every strategy live_vs_backtest_sync has not stored trades for.
INSERT INTO winrate_bt_trades
SELECT strategy_id, pass, symbol, exit_date, win, return_pct
FROM winrate_baseline_trade
WHERE strategy_id NOT IN (SELECT strategy_id FROM winrate_bt_trades);
