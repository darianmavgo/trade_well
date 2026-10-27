-- Why the model did or did not enter, for the latest five sessions. One row per session in
-- signal_trace, which a live scan reads and shows next to its ENTER or NO_SIGNAL. The entry
-- column is taken from the signals table above, so this explains the decision and never makes it.
-- trained_through is the last session the counts learned from, and scored_live says the session
-- is after it, so it was scored with those frozen counts.
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
        p.Date AS date,
        p.prob_bull,
        p.prob_bear,
        p.signal,
        CASE p.current_state WHEN 1 THEN 'bull' WHEN -1 THEN 'bear' ELSE 'sideways' END AS state,
        EXISTS (SELECT 1 FROM markov_model_signals s WHERE s.date = p.Date AND s.entry = 1) AS entered,
        CASE lower('__REGIME_LABEL__')
            WHEN 'bull' THEN p.signal > 0.10
            WHEN 'bear' THEN p.signal < -0.10
            ELSE abs(p.signal) <= 0.10
        END AS qualifies,
        CASE lower('__REGIME_LABEL__')
            WHEN 'bull' THEN 'signal > 0.10'
            WHEN 'bear' THEN 'signal < -0.10'
            ELSE 'signal within 0.10 of zero'
        END AS bound
    FROM markov_model_predictions p
    WHERE p.symbol = '__SYMBOL__'
      AND p.Date IN (SELECT Date FROM markov_model_predictions WHERE symbol = '__SYMBOL__' ORDER BY Date DESC LIMIT 5)
)
SELECT
    '__TRADE_SYMBOL__',
    r.date,
    r.entered,
    CASE
        WHEN r.entered THEN 'ENTER: state ' || r.state || ', signal ' || printf('%.2f', r.signal) || ' (' || r.bound || ')'
        WHEN r.state <> lower('__REGIME_LABEL__') THEN 'no entry: state is ' || r.state || ', the strategy needs ' || lower('__REGIME_LABEL__')
        WHEN r.qualifies THEN 'no entry: state and signal qualify but there is no __TRADE_SYMBOL__ bar for this date'
        ELSE 'no entry: state is ' || r.state || ' but signal ' || printf('%.2f', r.signal) || ' fails the rule (' || r.bound || ')'
    END,
    json_object(
        'model_symbol', '__SYMBOL__',
        'state', r.state,
        'prob_bull', round(r.prob_bull, 4),
        'prob_bear', round(r.prob_bear, 4),
        'signal', round(r.signal, 4),
        'rule', 'state ' || lower('__REGIME_LABEL__') || ' and ' || r.bound,
        'trained_through', (SELECT trained_through FROM markov.markov_model_meta WHERE symbol = '__SYMBOL__'),
        'scored_live', r.date > coalesce((SELECT trained_through FROM markov.markov_model_meta WHERE symbol = '__SYMBOL__'), '')
    )
FROM r
ORDER BY r.date;
