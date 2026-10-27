CREATE TABLE IF NOT EXISTS killswitch (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	engaged INTEGER NOT NULL DEFAULT 0,
	changed_at DATETIME,
	changed_by TEXT,
	reason TEXT
)
