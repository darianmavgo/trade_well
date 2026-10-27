-- Slice tables for a streak grid search on one watch symbol. Each later file
-- reads the table the one before it wrote. Requires bar_sma for the symbol.
DROP TABLE IF EXISTS streak_diff;
CREATE TABLE streak_diff (
    symbol TEXT, date TEXT, close REAL,
    sma50 REAL, sma200 REAL, is_down INTEGER, is_up INTEGER
);

DROP TABLE IF EXISTS streak_group;
CREATE TABLE streak_group (
    symbol TEXT, date TEXT, close REAL,
    sma50 REAL, sma200 REAL, is_down INTEGER, is_up INTEGER,
    down_grp INTEGER, up_grp INTEGER
);

DROP TABLE IF EXISTS streak_slice;
CREATE TABLE streak_slice (
    symbol TEXT, date TEXT, close REAL,
    sma50 REAL, sma200 REAL, down_streak INTEGER, up_streak INTEGER
);
CREATE INDEX IF NOT EXISTS idx_streak_slice_date ON streak_slice(date);

DROP TABLE IF EXISTS streak_entries;
CREATE TABLE streak_entries (
    trade_symbol TEXT, signal_days INTEGER, regime TEXT, date TEXT,
    open REAL, high REAL, low REAL, close REAL, volume INTEGER
);
CREATE INDEX IF NOT EXISTS idx_streak_entries ON streak_entries(trade_symbol, signal_days, regime, date);
