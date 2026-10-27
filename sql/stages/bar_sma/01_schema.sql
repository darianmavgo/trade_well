-- Slice table: 50- and 200-bar simple moving averages of close, one row per
-- symbol per date. Built in the calc database of the run that needs it.
-- A short history still yields the average of the bars that exist.
DROP TABLE IF EXISTS bar_sma;
CREATE TABLE bar_sma (
    symbol TEXT,
    date TEXT,
    sma50 REAL,
    sma200 REAL
);
CREATE INDEX IF NOT EXISTS idx_bar_sma ON bar_sma(symbol, date);
