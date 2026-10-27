CREATE TABLE IF NOT EXISTS hmm_prediction_live (
	symbol TEXT NOT NULL,
	trained_at TEXT NOT NULL,
	date TEXT NOT NULL,
	ret REAL NOT NULL,
	vol REAL NOT NULL,
	regime INTEGER NOT NULL,
	regime_prob REAL NOT NULL,
	next_regime INTEGER NOT NULL,
	next_regime_prob REAL NOT NULL,
	loglik_bar REAL NOT NULL,
	in_sample INTEGER NOT NULL,
	PRIMARY KEY (symbol, trained_at, date)
) WITHOUT ROWID
