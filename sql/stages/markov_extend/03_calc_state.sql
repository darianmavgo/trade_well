-- Bull at +5% or more over 20 bars, bear at -5% or less, otherwise sideways (same as markov_train).
INSERT INTO markov_x_state (symbol, date, state)
SELECT
    symbol, date,
    CASE WHEN ret_20d >= 0.05 THEN 1 WHEN ret_20d <= -0.05 THEN -1 ELSE 0 END
FROM markov_x_ret
WHERE ret_20d IS NOT NULL
