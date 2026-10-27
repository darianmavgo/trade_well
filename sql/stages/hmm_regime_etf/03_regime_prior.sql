-- The regime the model had at the previous session's close. That is the only regime a trader
-- holds when the next session opens, so it is the one that can be acted on. The regime of the same
-- session is not used: it is estimated from that session's own return.
DROP TABLE IF EXISTS regime_prior;

CREATE TABLE regime_prior AS
SELECT
    date,
    in_sample,
    LAG(regime) OVER (ORDER BY date) AS r_prior
FROM regime_day;
