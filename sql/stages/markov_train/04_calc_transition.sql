-- The state the next bar moved to. __THROUGH__ ('' = no cutoff) is the last session the
-- model may learn from: a transition whose destination bar is after it has no
-- to_state, so the walk-forward counts in the next stage stop growing there. Rows
-- after the cutoff are still scored (their own state is known), with the counts
-- frozen at the cutoff: the model never learns from the held-out period. __FROM__ ('' =
-- the start of the data) is the first session it may learn from: transitions out of
-- earlier bars are not counted. (Same rule as backtestgoduckdb's 04_calc_transition.)
INSERT INTO markov_transition (symbol, date, from_state, to_state)
SELECT
    symbol, date, state,
    CASE WHEN ('__THROUGH__' = '' OR LEAD(date) OVER w <= '__THROUGH__') AND ('__FROM__' = '' OR date >= '__FROM__')
         THEN LEAD(state) OVER w END
FROM markov_state
WINDOW w AS (PARTITION BY symbol ORDER BY date);
