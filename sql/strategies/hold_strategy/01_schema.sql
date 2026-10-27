-- Hold one symbol. __TRADE_SYMBOL__ is the symbol, __SMA_PERIOD__ the length of
-- the re-entry average (0 = never re-enter), __SMA_PRECEDING__ that length minus
-- one (0 when there is no average), __TOTAL_RETURN__ is 1 when the run simulates
-- on dividend-adjusted prices, and __START_DATE__ / __END_DATE__ are the window
-- of bars loaded.
DROP TABLE IF EXISTS hold_strategy_bars;
CREATE TABLE hold_strategy_bars (
    idx INTEGER, symbol TEXT, date TEXT,
    open REAL, high REAL, low REAL, close REAL, volume INTEGER,
    rn INTEGER, sma REAL
);

DROP TABLE IF EXISTS hold_strategy_signals;
CREATE TABLE hold_strategy_signals (
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
