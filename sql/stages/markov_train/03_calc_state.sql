-- Bull at +5% or more over 20 bars, bear at -5% or less, otherwise sideways.
-- Bars with no 20-bar history have no state.
INSERT INTO markov_state (symbol, date, close, ret_20d, state)
SELECT
    symbol, date, close, ret_20d,
    CASE WHEN ret_20d >= 0.05 THEN 1 WHEN ret_20d <= -0.05 THEN -1 ELSE 0 END
FROM markov_ret
WHERE ret_20d IS NOT NULL;
