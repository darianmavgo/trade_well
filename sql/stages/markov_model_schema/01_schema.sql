-- The persisted Markov model, one row per symbol per date. Written by
-- train markov and read (never recomputed) by markov strategies at backtest time.
-- state is 1 bull, -1 bear, 0 sideways. prob_bull and prob_bear are the
-- walk-forward chance that the next bar is bull or bear, from transitions
-- known as of that date. signal is prob_bull minus prob_bear.
CREATE TABLE IF NOT EXISTS markov_prediction (
    symbol TEXT NOT NULL,
    date TEXT NOT NULL,
    state INTEGER NOT NULL,
    prob_bull REAL NOT NULL,
    prob_bear REAL NOT NULL,
    signal REAL NOT NULL,
    PRIMARY KEY (symbol, date)
) WITHOUT ROWID;

-- One row per trained symbol: what the model was trained on and when.
CREATE TABLE IF NOT EXISTS markov_model_meta (
    symbol TEXT PRIMARY KEY,
    bars INTEGER NOT NULL,
    first_date TEXT NOT NULL,
    last_date TEXT NOT NULL,
    trained_at TEXT NOT NULL,
    lookback_days INTEGER NOT NULL,
    state_threshold REAL NOT NULL,
    trained_through TEXT                -- last session the model learned from (the in-sample end), NULL on models trained before it existed
);
