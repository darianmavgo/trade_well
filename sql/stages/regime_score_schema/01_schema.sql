-- Slice tables of one regime_score run. The run's database is created fresh, so nothing here
-- reads a table an earlier run left behind. report is the numbered run folder the trades came
-- from and win is 'in_sample' or 'oos'.
CREATE TABLE trade_all (
    report INTEGER, win TEXT, src TEXT, run_id INTEGER, strategy_id TEXT,
    entry_date TEXT, exit_date TEXT, entry_price REAL, net_pnl REAL, return_pct REAL,
    hold_days INTEGER, exit_reason TEXT
);

CREATE TABLE spy_session (date TEXT, state INTEGER, next_date TEXT);

CREATE TABLE trade_ranked (
    report INTEGER, win TEXT, src TEXT, strategy_id TEXT, entry_date TEXT, exit_date TEXT,
    entry_price REAL, net_pnl REAL, return_pct REAL, hold_days INTEGER, exit_reason TEXT, rn INTEGER
);

CREATE TABLE trade_dedup (
    report INTEGER, win TEXT, src TEXT, strategy_id TEXT, entry_date TEXT, exit_date TEXT,
    entry_price REAL, net_pnl REAL, return_pct REAL, hold_days INTEGER, exit_reason TEXT
);

CREATE TABLE trade_regime (
    report INTEGER, win TEXT, src TEXT, strategy_id TEXT, entry_date TEXT, exit_date TEXT,
    entry_price REAL, net_pnl REAL, return_pct REAL, hold_days INTEGER, exit_reason TEXT,
    regime_date TEXT, regime TEXT
);

CREATE TABLE strategy_all (strategy_id TEXT, win TEXT, n INTEGER, avg_return_pct REAL);

CREATE TABLE regime_score (
    strategy_id TEXT, win TEXT, regime TEXT, n INTEGER, wins INTEGER, win_rate REAL,
    avg_return_pct REAL, total_net_pnl REAL, profit_factor REAL, avg_hold_days REAL,
    thin INTEGER, vs_all_avg_return_pct REAL
);

CREATE TABLE regime_pooled (
    win TEXT, regime TEXT, strategies INTEGER, n INTEGER, wins INTEGER, win_rate REAL,
    avg_return_pct REAL, total_net_pnl REAL, profit_factor REAL, avg_hold_days REAL, thin INTEGER
);

CREATE TABLE regime_coverage (what TEXT, n INTEGER)
