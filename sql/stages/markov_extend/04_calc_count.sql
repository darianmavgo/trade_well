-- The model's frozen counts, rebuilt from its own saved states: a transition counts when its
-- destination session is on or before the model's trained_through (last_date for a model saved
-- before that column existed). This is the same rule markov_train uses for its cutoff, so the
-- counts equal the ones the saved predictions after the cutoff were scored with.
INSERT INTO markov_x_count (symbol, state, to_bull, to_bear, total)
SELECT
    t.symbol, t.state,
    SUM(CASE WHEN t.next_state = 1 THEN 1 ELSE 0 END),
    SUM(CASE WHEN t.next_state = -1 THEN 1 ELSE 0 END),
    COUNT(*)
FROM (
    SELECT symbol, state,
           LEAD(state) OVER w AS next_state,
           LEAD(date) OVER w AS next_date
    FROM model.markov_prediction
    WHERE symbol IN (__SYMBOL_LIST__)
    WINDOW w AS (PARTITION BY symbol ORDER BY date)
) t
JOIN model.markov_model_meta m ON m.symbol = t.symbol
WHERE t.next_state IS NOT NULL AND t.next_date <= COALESCE(m.trained_through, m.last_date)
GROUP BY t.symbol, t.state
