CREATE TABLE IF NOT EXISTS risk_equity (
	account_number TEXT NOT NULL,
	session_date TEXT NOT NULL,
	equity REAL NOT NULL,
	recorded_at DATETIME NOT NULL,
	PRIMARY KEY (account_number, session_date)
);
CREATE TABLE IF NOT EXISTS risk_state (
	account_number TEXT PRIMARY KEY,
	assessed_at DATETIME NOT NULL,
	session_date TEXT NOT NULL,
	equity REAL NOT NULL,
	peak REAL NOT NULL,
	drawdown REAL NOT NULL,
	day_change REAL NOT NULL,
	gross REAL NOT NULL,
	scale REAL NOT NULL,
	halt_entries INTEGER NOT NULL,
	max_gross REAL NOT NULL,
	reasons TEXT NOT NULL
)
