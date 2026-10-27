-- Walk-forward probabilities of the next bar being bull or bear.
-- A symbol needs at least 60 in-sample transitions (about three months of sessions
-- that end inside the training window) to get a model: with fewer the counts are
-- empty and every probability would be 0, a model in name only.
INSERT INTO markov_batch_prediction (symbol, date, state, prob_bull, prob_bear, signal)
SELECT
    symbol, date, from_state,
    CASE WHEN total_transitions > 0 THEN CAST(cum_to_bull AS REAL) / total_transitions ELSE 0.0 END,
    CASE WHEN total_transitions > 0 THEN CAST(cum_to_bear AS REAL) / total_transitions ELSE 0.0 END,
    CASE WHEN total_transitions > 0
         THEN (CAST(cum_to_bull AS REAL) - CAST(cum_to_bear AS REAL)) / total_transitions
         ELSE 0.0 END
FROM markov_cumulative
WHERE symbol IN (SELECT symbol FROM markov_transition GROUP BY symbol HAVING count(to_state) >= 60);
