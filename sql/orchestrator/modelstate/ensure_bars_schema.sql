CREATE TABLE IF NOT EXISTS daily_bars_live (
	symbol TEXT NOT NULL,
	date TEXT NOT NULL,
	close REAL NOT NULL,
	adj_close REAL NOT NULL,
	volume INTEGER NOT NULL,
	source TEXT NOT NULL,
	PRIMARY KEY (symbol, date)
) WITHOUT ROWID
