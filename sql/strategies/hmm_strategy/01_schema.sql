-- The signals the hmm family returns. The calc database is new for every run.
DROP TABLE IF EXISTS hmm_strategy_signals;

CREATE TABLE hmm_strategy_signals (
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
