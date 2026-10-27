-- Forward-filtered probability of the stressed regime on each date. It uses only bars up to the
-- date, so it is known at that session's close.
DROP TABLE IF EXISTS hmm_stress_prob;

CREATE TABLE hmm_stress_prob AS
SELECT
    p.date AS Date,
    p.prob AS stress_prob,
    p.next_prob AS next_stress_prob
FROM hmm.hmm_state_prob p
JOIN hmm_stress_state s ON s.symbol = p.symbol AND s.state = p.state
WHERE p.symbol = '__SYMBOL__';
