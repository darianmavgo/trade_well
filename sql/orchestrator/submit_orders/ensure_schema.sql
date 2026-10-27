CREATE TABLE IF NOT EXISTS submit_order (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	staged_order_id INTEGER NOT NULL,
	broker_order_id TEXT,
	symbol TEXT NOT NULL,
	quantity REAL NOT NULL,
	status TEXT NOT NULL,
	detail TEXT,
	submitted_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_submit_order_staged ON submit_order(staged_order_id);
