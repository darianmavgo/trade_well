-- Decision tree features for one symbol (__SYMBOL__), one slice table per stage.
-- Each file after this one reads the table the one before it wrote.
DROP TABLE IF EXISTS tf_base;
CREATE TABLE tf_base (
    idx INTEGER, symbol TEXT, date TEXT,
    open REAL, high REAL, low REAL, close REAL, volume INTEGER,
    prev_close REAL, prev_close_3 REAL, prev_close_5 REAL, prev_close_10 REAL,
    next_close REAL, rn INTEGER
);

DROP TABLE IF EXISTS tf_chg;
CREATE TABLE tf_chg (
    idx INTEGER, symbol TEXT, date TEXT,
    open REAL, high REAL, low REAL, close REAL, volume INTEGER,
    prev_close REAL, prev_close_3 REAL, prev_close_5 REAL, prev_close_10 REAL,
    next_close REAL, rn INTEGER,
    chg REAL, true_range REAL, is_down INTEGER, down_grp INTEGER
);

DROP TABLE IF EXISTS tf_win;
CREATE TABLE tf_win (
    date TEXT, open REAL, high REAL, low REAL, close REAL, volume INTEGER,
    prev_close REAL, prev_close_3 REAL, prev_close_5 REAL, prev_close_10 REAL,
    next_close REAL, rn INTEGER,
    sma20 REAL, sma50 REAL, sma200 REAL,
    avg_gain14 REAL, avg_loss14 REAL, vol_avg20 REAL, atr14 REAL, down_streak REAL
);

-- next_return is the next bar's return in percent and is empty on the last bar.
-- class is the 5 bucket label of next_return: -2 at or below -5, -1 above -5 and
-- below -1, 0 from -1 to 1 inclusive, 1 above 1 and below 5, 2 at or above 5.
-- It is empty on the last bar, which is still given features so it can be scored.
DROP TABLE IF EXISTS decision_tree_features_slice;
CREATE TABLE decision_tree_features_slice (
    date TEXT,
    close REAL,
    next_return REAL,
    class INTEGER,
    return_1d REAL,
    return_3d REAL,
    return_5d REAL,
    return_10d REAL,
    rsi14 REAL,
    price_vs_sma20 REAL,
    price_vs_sma50 REAL,
    price_vs_sma200 REAL,
    sma20_vs_50 REAL,
    vol_ratio20 REAL,
    range_vs_atr14 REAL,
    close_near_high REAL,
    consecutive_down INTEGER
);
CREATE INDEX IF NOT EXISTS idx_dtf_date ON decision_tree_features_slice(date);
