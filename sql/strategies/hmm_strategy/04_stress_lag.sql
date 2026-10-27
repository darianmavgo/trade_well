-- The stressed probability of the session before, for the calm_down rule.
DROP TABLE IF EXISTS hmm_stress_lag;

CREATE TABLE hmm_stress_lag AS
SELECT
    Date,
    stress_prob,
    next_stress_prob,
    LAG(stress_prob) OVER (ORDER BY Date) AS prev_stress_prob
FROM hmm_stress_prob;
