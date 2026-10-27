-- Stage 11 when live_vs_backtest_sync has never run.
DROP TABLE IF EXISTS winrate_bt_trades;
CREATE TABLE winrate_bt_trades (strategy_id TEXT, pass TEXT, symbol TEXT, exit_date TEXT, win INTEGER, return_pct REAL);
