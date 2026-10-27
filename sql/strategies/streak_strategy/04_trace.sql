-- Why the streak did or did not enter, for the latest five sessions. One row per session in
-- signal_trace, which a live scan reads and shows next to its ENTER or NO_SIGNAL. The entry
-- column is taken from the signals table above, so this explains the decision and never makes it.
CREATE TABLE IF NOT EXISTS signal_trace (
    symbol TEXT,
    date TEXT,
    entry INTEGER,
    reason TEXT,
    facts TEXT
);

INSERT INTO signal_trace (symbol, date, entry, reason, facts)
WITH r AS (
    SELECT
        v.date,
        v.close,
        v.__STREAK_COL__ AS streak,
        v.sma200,
        EXISTS (SELECT 1 FROM streak_strategy_signals s WHERE s.date = v.date AND s.entry = 1) AS entered
    FROM streak_strategy_slice v
    WHERE v.date IN (SELECT date FROM streak_strategy_slice ORDER BY date DESC LIMIT 5)
)
SELECT
    '__TRADE_SYMBOL__',
    r.date,
    r.entered,
    CASE
        WHEN r.entered THEN 'ENTER: __SIGNAL_SYMBOL__ ' || r.streak || ' day __STREAK_COL__ (needs __DECLINE_DAYS__)'
        WHEN r.streak < __DECLINE_DAYS__ THEN 'no entry: __SIGNAL_SYMBOL__ __STREAK_COL__ is ' || r.streak || ', needs __DECLINE_DAYS__'
        ELSE 'no entry: the streak qualifies but the regime filter or the __TRADE_SYMBOL__ bar blocked it'
    END,
    json_object(
        'signal_symbol', '__SIGNAL_SYMBOL__',
        'close', r.close,
        'streak_column', '__STREAK_COL__',
        'streak', r.streak,
        'needs', __DECLINE_DAYS__,
        'regime', '__REGIME_LABEL__'
    )
FROM r
ORDER BY r.date;
