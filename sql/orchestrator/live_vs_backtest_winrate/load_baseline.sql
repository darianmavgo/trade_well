DROP TABLE IF EXISTS winrate_baseline;
CREATE TABLE winrate_baseline (strategy_id TEXT, is_trades INTEGER, is_win_rate REAL, oos_trades INTEGER, oos_win_rate REAL,
	all_trades INTEGER, all_win_rate REAL, is_start TEXT, is_end TEXT, oos_start TEXT, oos_end TEXT, evaluated_at TEXT, all_evaluated_at TEXT);
DROP TABLE IF EXISTS winrate_baseline_trade;
CREATE TABLE winrate_baseline_trade (strategy_id TEXT, pass TEXT, symbol TEXT, exit_date TEXT, win INTEGER, return_pct REAL)
