-- Transition counts per from-state over every earlier bar. The frame ends one
-- row before the current one, so only transitions fully known as of the date
-- count (the current row transitions into the future).
INSERT INTO markov_cumulative (symbol, date, from_state, cum_to_bull, cum_to_bear, cum_to_sideways, total_transitions)
SELECT
    symbol, date, from_state,
    SUM(CASE WHEN to_state = 1 THEN 1 ELSE 0 END) OVER w,
    SUM(CASE WHEN to_state = -1 THEN 1 ELSE 0 END) OVER w,
    SUM(CASE WHEN to_state = 0 THEN 1 ELSE 0 END) OVER w,
    COUNT(to_state) OVER w
FROM markov_transition
WINDOW w AS (PARTITION BY symbol, from_state ORDER BY date ROWS BETWEEN UNBOUNDED PRECEDING AND 1 PRECEDING);
