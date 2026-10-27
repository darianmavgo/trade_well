CREATE TABLE IF NOT EXISTS markov_model_signals (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    idx INTEGER,
    symbol TEXT,
    date TEXT,
    open FLOAT,
    high FLOAT,
    low FLOAT,
    close FLOAT,
    volume BIGINT,
    buylimit FLOAT,
    entry INTEGER,
    direction TEXT,
    regime TEXT,
    hold_days_override INTEGER,
    take_profit FLOAT,
    stop_loss FLOAT,
    allocation_pct_override FLOAT
);
