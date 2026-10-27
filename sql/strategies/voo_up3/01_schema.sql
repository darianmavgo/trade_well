-- Schema for the VOO N-day up-streak -> trade-symbol strategy pipeline.
DROP TABLE IF EXISTS voo_up3_streaks_slice;
CREATE TABLE voo_up3_streaks_slice (
    date TEXT,
    up_streak INTEGER
);
CREATE INDEX IF NOT EXISTS idx_voo_up3_streaks_date ON voo_up3_streaks_slice(date);

DROP TABLE IF EXISTS voo_up3_signals;
CREATE TABLE voo_up3_signals (
    idx INTEGER,
    symbol TEXT,
    date TEXT,
    open REAL,
    high REAL,
    low REAL,
    close REAL,
    volume INTEGER,
    buylimit REAL,
    entry INTEGER DEFAULT 0,
    direction TEXT,
    regime TEXT,
    hold_days_override INTEGER,
    take_profit REAL,
    stop_loss REAL
);
CREATE INDEX IF NOT EXISTS idx_voo_up3_signals ON voo_up3_signals(symbol, date);
