-- Stage 2 when no refresh has ever run: no stored backtest rows.
DROP TABLE IF EXISTS winrate_backtest;
CREATE TABLE winrate_backtest (
	strategy_id TEXT, is_trades INTEGER, is_win_rate REAL, oos_trades INTEGER, oos_win_rate REAL,
	all_trades INTEGER, all_win_rate REAL, all_evaluated_at TEXT,
	is_start TEXT, is_end TEXT, oos_start TEXT, oos_end TEXT, evaluated_at TEXT, source TEXT);
