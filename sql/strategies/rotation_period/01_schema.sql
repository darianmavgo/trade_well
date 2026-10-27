-- Period winner: at the start of each calendar period, hold the best __TOP_K__
-- names by the previous period's return until that period's last session.
-- Parameters (set from the rotation_strategy row):
--   __SIDE__         long, short or inverse (the matched inverse ETF of each pick).
--   __PERIOD_KEY__   SQL expression of `date` naming the calendar period of a
--                    session (1d, 1w, 1m, 1q or 1y). Keys sort in time order.
--   __TOP_K__        names held each period.
--   __PICK_LIMIT__   names picked each period: __TOP_K__, or every name of the list for a
--                    row that picks all.
--   __PERIOD_EXIT__  1 sells at the period's last session, 0 leaves the exit to the row's
--                    hold window, take profit and stop (a 1d row with a hold window).
--   __PICK_ORDER__   DESC to hold the best previous period returns, ASC the worst.
--   __USE_LIST__ / __SYMBOLS__  1 and a quoted list restrict the candidates, 0 means
--                    every symbol in the market database.
--   __ALLOC__        share of equity per name.
--   __START_DATE__ and __END_DATE__ are the window of bars loaded.
-- Each file after this one reads the table the one before it wrote.
DROP TABLE IF EXISTS rp_bar;
CREATE TABLE rp_bar (
    idx INTEGER, symbol TEXT, period TEXT, date TEXT,
    open REAL, high REAL, low REAL, close REAL, volume INTEGER,
    rn_first INTEGER, rn_last INTEGER, next_date TEXT
);
CREATE INDEX IF NOT EXISTS idx_rp_bar ON rp_bar(symbol, period, date);

DROP TABLE IF EXISTS rp_period;
CREATE TABLE rp_period (
    symbol TEXT, period TEXT, first_date TEXT, first_open REAL, last_date TEXT, last_close REAL, ret REAL
);
CREATE INDEX IF NOT EXISTS idx_rp_period ON rp_period(period, symbol);

DROP TABLE IF EXISTS rp_seq;
CREATE TABLE rp_seq (period TEXT PRIMARY KEY, seq INTEGER);

DROP TABLE IF EXISTS rp_pick;
CREATE TABLE rp_pick (period TEXT, symbol TEXT, ret REAL);

DROP TABLE IF EXISTS rp_trade;
CREATE TABLE rp_trade (period TEXT, trade_symbol TEXT, entry_date TEXT);

DROP TABLE IF EXISTS rp_inverse;
CREATE TABLE rp_inverse (symbol TEXT PRIMARY KEY, inverse TEXT);

DROP TABLE IF EXISTS rotation_period_signals;
CREATE TABLE rotation_period_signals (
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
