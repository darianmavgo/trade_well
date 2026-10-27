-- One entry on every bar of one symbol, each with its own take-profit, stop and
-- hold. __TRADE_SYMBOL__ is the symbol and __START_DATE__ / __END_DATE__ are the
-- window of bars loaded.
DROP TABLE IF EXISTS every_bar_signals;
CREATE TABLE every_bar_signals (
    idx INTEGER,
    symbol TEXT,
    date TEXT,
    open REAL,
    high REAL,
    low REAL,
    close REAL,
    volume INTEGER,
    buylimit REAL,
    entry INTEGER,
    direction TEXT,
    regime TEXT,
    hold_days_override INTEGER,
    take_profit REAL,
    stop_loss REAL,
    allocation_pct_override REAL
);
