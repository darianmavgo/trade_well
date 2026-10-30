-- Slice tables the gate and day stages append to, one source (a family's finished backtest) at a time. Created empty by runner.PickSet.
DROP TABLE IF EXISTS pick_candidate;

CREATE TABLE pick_candidate (
    src INTEGER,
    strategy_id TEXT,
    is_win_rate REAL,
    is_trades INTEGER,
    is_cagr REAL,
    is_dd REAL,
    oos_win_rate REAL,
    oos_trades INTEGER,
    oos_cagr REAL,
    oos_dd REAL,
    score REAL
);

DROP TABLE IF EXISTS pick_exclude;

CREATE TABLE pick_exclude (strategy_id TEXT PRIMARY KEY);

DROP TABLE IF EXISTS pick_entry_day;

CREATE TABLE pick_entry_day (strategy_id TEXT, entry_date TEXT)
