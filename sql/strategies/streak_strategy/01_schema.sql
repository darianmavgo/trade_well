-- One pipeline for every streak_strategy row. Tables are recreated per run.
DROP TABLE IF EXISTS streak_strategy_slice;
CREATE TABLE streak_strategy_slice (
    idx INTEGER,
    symbol TEXT,
    date TEXT,
    open REAL,
    high REAL,
    low REAL,
    close REAL,
    volume INTEGER,
    sma200 REAL,
    down_streak INTEGER,
    up_streak INTEGER
);
CREATE INDEX IF NOT EXISTS idx_streak_strategy_slice_date ON streak_strategy_slice(date);

DROP TABLE IF EXISTS streak_strategy_signals;
CREATE TABLE streak_strategy_signals (
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
CREATE INDEX IF NOT EXISTS idx_streak_strategy_signals ON streak_strategy_signals(symbol, date);
