-- Price action reclaim of one symbol. __TRADE_SYMBOL__ is the symbol, __SMA_PRECEDING__ the
-- length of the trend average minus one, __SUPPORT_BARS__ and __RECLAIM_GAP__ the support window
-- (the lowest low from that many bars back to that many bars back), __STOP_CUSHION_MULT__ one
-- minus the stop cushion, __MAX_STOP_RATIO__ one minus the widest stop, and __START_DATE__ /
-- __END_DATE__ the window of bars the run loaded. Each file after this one reads the table the
-- one before it wrote.
DROP TABLE IF EXISTS par_sma;
CREATE TABLE par_sma (
    idx INTEGER, symbol TEXT, date TEXT,
    open REAL, high REAL, low REAL, close REAL, volume INTEGER, sma REAL
);

DROP TABLE IF EXISTS par_window;
CREATE TABLE par_window (
    idx INTEGER, symbol TEXT, date TEXT,
    open REAL, high REAL, low REAL, close REAL, volume INTEGER, sma REAL,
    rn INTEGER, prev_close REAL, support REAL
);

DROP TABLE IF EXISTS price_action_reclaim_strategy_signals;
CREATE TABLE price_action_reclaim_strategy_signals (
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
