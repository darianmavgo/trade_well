-- Price action reclaim over every symbol with enough bars. __START_DATE__ and
-- __END_DATE__ are the window of bars the run loaded. Each file after this one
-- reads the table the one before it wrote.
DROP TABLE IF EXISTS par_symbol;
CREATE TABLE par_symbol (symbol TEXT PRIMARY KEY);

DROP TABLE IF EXISTS par_sma;
CREATE TABLE par_sma (
    idx INTEGER, symbol TEXT, date TEXT,
    open REAL, high REAL, low REAL, close REAL, volume INTEGER, sma200 REAL
);

DROP TABLE IF EXISTS par_window;
CREATE TABLE par_window (
    idx INTEGER, symbol TEXT, date TEXT,
    open REAL, high REAL, low REAL, close REAL, volume INTEGER, sma200 REAL,
    rn INTEGER, prev_close REAL, support REAL
);

DROP TABLE IF EXISTS price_action_reclaim_signals;
CREATE TABLE price_action_reclaim_signals (
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
