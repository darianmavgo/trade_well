-- Stage 20 when trade_history does not exist yet.
DROP TABLE IF EXISTS winrate_recent;
CREATE TABLE winrate_recent (strategy_id TEXT, account_number TEXT, symbol TEXT, status TEXT, exit_reason TEXT, exit_reason_code TEXT,
	entry_time TEXT, exit_time TEXT, exit_date TEXT, quantity REAL, entry_price REAL, entry_fill_price REAL, exit_fill_price REAL, exit_price REAL, pnl REAL, pnl_pct REAL, stop_loss INTEGER);
