-- The persisted HMM, written by train hmm and read (never refit) by the daily prediction.
-- States are sorted by mean return: state 0 is the lowest-return regime and the last state
-- the highest. Every probability is forward-filtered, so a row uses only bars up to its date.

-- One row per trained symbol.
CREATE TABLE IF NOT EXISTS hmm_model_meta (
    symbol TEXT PRIMARY KEY,
    states INTEGER NOT NULL,
    dims INTEGER NOT NULL,
    vol_window INTEGER NOT NULL,
    bars INTEGER NOT NULL,              -- observations the fit learned from
    first_date TEXT NOT NULL,           -- first observation date
    last_date TEXT NOT NULL,            -- last date with a prediction row
    trained_through TEXT NOT NULL,      -- last session the fit learned from (the in-sample end)
    trained_at TEXT NOT NULL,
    loglik REAL NOT NULL,
    bic REAL NOT NULL,
    source TEXT NOT NULL
);

-- Initial probability, mean vector and 2x2 covariance of each state.
CREATE TABLE IF NOT EXISTS hmm_params (
    symbol TEXT NOT NULL,
    state INTEGER NOT NULL,
    pi REAL NOT NULL,
    mu_ret REAL NOT NULL,
    mu_vol REAL NOT NULL,
    var_ret REAL NOT NULL,
    cov_ret_vol REAL NOT NULL,
    var_vol REAL NOT NULL,
    PRIMARY KEY (symbol, state)
) WITHOUT ROWID;

-- A[from][to] = chance the next session is in state to, given state from.
CREATE TABLE IF NOT EXISTS hmm_transition (
    symbol TEXT NOT NULL,
    from_state INTEGER NOT NULL,
    to_state INTEGER NOT NULL,
    prob REAL NOT NULL,
    PRIMARY KEY (symbol, from_state, to_state)
) WITHOUT ROWID;

-- Filtered probability of each state on each date, and the chance of each state next session.
CREATE TABLE IF NOT EXISTS hmm_state_prob (
    symbol TEXT NOT NULL,
    date TEXT NOT NULL,
    state INTEGER NOT NULL,
    prob REAL NOT NULL,
    next_prob REAL NOT NULL,
    PRIMARY KEY (symbol, date, state)
) WITHOUT ROWID;

-- One row per date: what was observed, the most probable regime, and the next-session call.
-- in_sample is 1 for dates the fit learned from and 0 for held-out and live dates.
CREATE TABLE IF NOT EXISTS hmm_prediction (
    symbol TEXT NOT NULL,
    date TEXT NOT NULL,
    ret REAL NOT NULL,
    vol REAL NOT NULL,
    regime INTEGER NOT NULL,
    regime_prob REAL NOT NULL,
    next_regime INTEGER NOT NULL,
    next_regime_prob REAL NOT NULL,
    loglik_bar REAL NOT NULL,
    in_sample INTEGER NOT NULL,
    PRIMARY KEY (symbol, date)
) WITHOUT ROWID;
