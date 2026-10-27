-- Stage 5 when live_vs_backtest_trades has never run.
DROP TABLE IF EXISTS winrate_model;
CREATE TABLE winrate_model (strategy_id TEXT, model_n INTEGER, model_wins INTEGER);
