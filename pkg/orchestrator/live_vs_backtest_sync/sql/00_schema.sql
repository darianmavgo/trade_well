-- Stage 0: the two input tables of one sync, filled from the backtest result files. They are
-- ordinary tables in Turso, named for this run by runtables, so a run can be audited afterwards.
DROP TABLE IF EXISTS bs_found;
CREATE TABLE bs_found (strategy_id TEXT, pass TEXT, run INTEGER, path TEXT, rel TEXT,
	trades INTEGER, win_rate REAL, window_start TEXT, window_end TEXT);

DROP TABLE IF EXISTS bs_trades;
CREATE TABLE bs_trades (strategy_id TEXT, pass TEXT, run INTEGER, symbol TEXT, exit_date TEXT, win INTEGER, return_pct REAL);
