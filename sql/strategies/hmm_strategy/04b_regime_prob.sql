-- Forward-filtered probability of each volatility rank on each date. Like the stressed
-- probability it uses only bars up to the date. The normal_filter rule enters when the middle
-- rank is the most likely regime.
DROP TABLE IF EXISTS hmm_regime_prob;

CREATE TABLE hmm_regime_prob AS
SELECT
    p.date AS Date,
    SUM(CASE WHEN r.vol_rank = 0 THEN p.prob END) AS calm_prob,
    SUM(CASE WHEN r.vol_rank = 1 THEN p.prob END) AS normal_prob,
    SUM(CASE WHEN r.vol_rank = 2 THEN p.prob END) AS stress_prob
FROM hmm.hmm_state_prob p
JOIN hmm_state_rank r ON r.symbol = p.symbol AND r.state = p.state
WHERE p.symbol = '__SYMBOL__'
GROUP BY p.date;
