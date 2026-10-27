-- Result table, one row per symbol and window. Kept across runs, a rerun replaces the rows of its window.
CREATE TABLE IF NOT EXISTS avg_volume (
    symbol TEXT NOT NULL,
    window_days INTEGER NOT NULL,
    avg_volume REAL NOT NULL,
    bars_used INTEGER NOT NULL,
    first_date TEXT NOT NULL,
    last_date TEXT NOT NULL,
    computed_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (symbol, window_days)
);

CREATE INDEX IF NOT EXISTS idx_avg_volume_window ON avg_volume(window_days, avg_volume)
