-- One row per regime-symbol session, with the next session's date. A trade entered after date
-- and up to next_date was initiated after this session closed, so this session is its regime.
-- The last session has no next date: it covers every later entry.
INSERT INTO spy_session (date, state, next_date)
SELECT date, state, COALESCE(LEAD(date) OVER (ORDER BY date), '9999-12-31')
FROM model.markov_prediction
WHERE symbol = '__SYMBOL__'
