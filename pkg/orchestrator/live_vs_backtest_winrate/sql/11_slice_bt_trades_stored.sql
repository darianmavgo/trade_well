-- Stage 11: the backtest trades kept by live_vs_backtest_sync.
DROP TABLE IF EXISTS winrate_bt_trades;
CREATE TABLE winrate_bt_trades AS
SELECT strategy_id, pass, symbol, exit_date, win, return_pct
FROM live_vs_backtest_deployed_trade;
