CREATE TABLE IF NOT EXISTS hmm_state_prob_live (
	symbol TEXT NOT NULL,
	trained_at TEXT NOT NULL,
	date TEXT NOT NULL,
	state INTEGER NOT NULL,
	prob REAL NOT NULL,
	next_prob REAL NOT NULL,
	PRIMARY KEY (symbol, trained_at, date, state)
) WITHOUT ROWID
