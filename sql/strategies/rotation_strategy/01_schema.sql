-- Rotation: every session, rank the liquid universe on momentum and hold the top
-- few names, rotating out of a name once it falls well down the ranking. This
-- is the shape of the Omnifunds account (docs/ImitatingOmnifunds.md), built from
-- bars only: no earnings filter and no trained model yet.
--
-- Parameters (set from the rotation_strategy row):
--   __USE_LIST__ / __SYMBOLS__  1 and a quoted, comma separated list restrict the
--                               candidates to those symbols, 0 means every symbol
--                               in the market database.
--   __UNIVERSE_SIZE__           the N most liquid names that session (60 session
--                               average dollar volume, known at that close).
--   __TOP_K__                   names to hold.
--   __EXIT_RANK__               a held name is sold once its rank is worse than this
--                               (top_k + exit_buffer), so rank churn near the cut
--                               does not trade.
--   __REGIME_ON__               1 to hold only while __REGIME_SYMBOL__ closes above
--                               its __REGIME_SMA__ session average, 0 for no gate.
--   __ALLOC__                   share of equity per name (min of the weight cap and
--                               allocation_pct / top_k).
--   __START_DATE__ / __END_DATE__  the window of bars loaded for the run.
--
-- Dates are the bar's own session, a signal on date D is decided on D's close.
DROP TABLE IF EXISTS rot_feature;
CREATE TABLE rot_feature (
    idx INTEGER, symbol TEXT, date TEXT,
    open REAL, high REAL, low REAL, close REAL, volume INTEGER,
    r1 REAL, r21 REAL, r63 REAL, r126 REAL, ma50 REAL, dd63 REAL,
    dv60 REAL, hist INTEGER
);

DROP TABLE IF EXISTS rot_universe;
CREATE TABLE rot_universe (
    symbol TEXT, date TEXT, liq_rank INTEGER
);

DROP TABLE IF EXISTS rot_rank;
CREATE TABLE rot_rank (
    symbol TEXT, date TEXT, score REAL, rank INTEGER
);

DROP TABLE IF EXISTS rot_regime;
CREATE TABLE rot_regime (
    date TEXT PRIMARY KEY, risk_on INTEGER
);

DROP TABLE IF EXISTS rot_state;
CREATE TABLE rot_state (
    symbol TEXT, date TEXT, held INTEGER, prev_held INTEGER
);

DROP TABLE IF EXISTS rotation_strategy_signals;
CREATE TABLE rotation_strategy_signals (
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
