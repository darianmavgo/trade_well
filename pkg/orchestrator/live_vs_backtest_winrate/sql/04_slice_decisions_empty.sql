-- Stage 4a when manage_exit does not exist yet.
DROP TABLE IF EXISTS winrate_decisions;
CREATE TABLE winrate_decisions (strategy_id TEXT, symbol TEXT, entry_day TEXT, fills INTEGER, pnl REAL, pnl_pct REAL, exit_time TEXT, id INTEGER, win INTEGER);
