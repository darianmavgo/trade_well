-- Two ways to decide the entries. __REGIME_ON__ is 1 when the row names a
-- regime symbol, 0 otherwise, exactly one of the inserts below returns rows.
--
-- Plain: enter on the first bar. When __SMA_PERIOD__ is above 0, also enter on
-- every bar after the average is full whose close is above it. A trailing stop,
-- if the row has one, is the simulator's exit and nothing here sells.
--
-- Regime: be long unless the Markov state of __REGIME_SYMBOL__ is bear. Each run
-- of consecutive non-bear bars is one holding period: one entry on its first
-- bar, with hold_days_override set so the simulator sells at the close of the
-- first bear bar after it. The state of a date uses that date's close and
-- earlier only, so the sale is the first one a live run could make. A bar with
-- no stored state counts as not bear. A run that reaches the last bar is not
-- sold (override 0).
-- __REGIME_DB__ is the Markov models database, or :memory: when the row has no
-- regime, so a plain hold never opens or creates the real file.
ATTACH DATABASE '__REGIME_DB__' AS markov;
CREATE TABLE IF NOT EXISTS markov.markov_prediction (
    symbol TEXT NOT NULL, date TEXT NOT NULL, state INTEGER NOT NULL,
    prob_bull REAL NOT NULL, prob_bear REAL NOT NULL, signal REAL NOT NULL,
    PRIMARY KEY (symbol, date)
) WITHOUT ROWID;

INSERT INTO hold_strategy_signals (idx, symbol, date, open, high, low, close, volume, buylimit, entry, direction, regime, hold_days_override, take_profit, stop_loss, allocation_pct_override)
SELECT
    idx, symbol, date, open, high, low, close, volume,
    close,
    1,
    'LONG',
    CASE WHEN __SMA_PERIOD__ > 0 THEN 'Close>SMA__SMA_PERIOD__' ELSE 'All Regimes' END,
    0,
    0.0,
    0.0,
    0.0
FROM hold_strategy_bars
WHERE __REGIME_ON__ = 0 AND (rn = 1 OR (__SMA_PERIOD__ > 0 AND rn > __SMA_PERIOD__ AND close > sma))
ORDER BY date;

INSERT INTO hold_strategy_signals (idx, symbol, date, open, high, low, close, volume, buylimit, entry, direction, regime, hold_days_override, take_profit, stop_loss, allocation_pct_override)
WITH flagged AS (
    SELECT b.*, CASE WHEN p.state = -1 THEN 1 ELSE 0 END AS bear
    FROM hold_strategy_bars b
    LEFT JOIN markov.markov_prediction p ON p.symbol = '__REGIME_SYMBOL__' AND p.date = b.date
),
grouped AS (
    -- a bear bar opens a new group, so the non-bear bars after it share its group
    SELECT flagged.*, SUM(bear) OVER (ORDER BY date) AS grp FROM flagged
),
runs AS (
    SELECT grouped.*,
        ROW_NUMBER() OVER (PARTITION BY grp, bear ORDER BY date) AS pos,
        COUNT(*) OVER (PARTITION BY grp, bear) AS len,
        MAX(grp) OVER () AS last_grp
    FROM grouped
)
SELECT
    idx, symbol, date, open, high, low, close, volume,
    close,
    1,
    'LONG',
    'Markov not bear',
    CASE WHEN grp < last_grp THEN len ELSE 0 END,
    0.0,
    0.0,
    0.0
FROM runs
WHERE __REGIME_ON__ = 1 AND bear = 0 AND pos = 1
ORDER BY date;
