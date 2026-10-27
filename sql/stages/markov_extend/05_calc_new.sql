-- Predictions for the sessions after the model's last_date, from the frozen counts of each state.
INSERT INTO markov_x_new (symbol, date, state, prob_bull, prob_bear, signal)
SELECT
    s.symbol, s.date, s.state,
    CASE WHEN c.total > 0 THEN CAST(c.to_bull AS REAL) / c.total ELSE 0.0 END,
    CASE WHEN c.total > 0 THEN CAST(c.to_bear AS REAL) / c.total ELSE 0.0 END,
    CASE WHEN c.total > 0 THEN (CAST(c.to_bull AS REAL) - CAST(c.to_bear AS REAL)) / c.total ELSE 0.0 END
FROM markov_x_state s
JOIN model.markov_model_meta m ON m.symbol = s.symbol
LEFT JOIN markov_x_count c ON c.symbol = s.symbol AND c.state = s.state
WHERE s.date > m.last_date
