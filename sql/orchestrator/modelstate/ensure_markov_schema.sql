CREATE TABLE IF NOT EXISTS markov_extension_live (
	symbol TEXT NOT NULL,
	trained_at TEXT NOT NULL,
	date TEXT NOT NULL,
	state INTEGER NOT NULL,
	prob_bull REAL NOT NULL,
	prob_bear REAL NOT NULL,
	signal REAL NOT NULL,
	PRIMARY KEY (symbol, trained_at, date)
) WITHOUT ROWID
