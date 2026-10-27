-- The model's regimes ranked by fitted volatility: 0 is the calmest, the highest rank the most
-- stressed. States are numbered by mean return, which is not stable between refits, so no state
-- number is used past this stage.
DROP TABLE IF EXISTS vol_rank;

CREATE TABLE vol_rank AS
SELECT state, RANK() OVER (ORDER BY mu_vol) - 1 AS vol_rank
FROM hmm.hmm_params
WHERE symbol = '__HMM_SYMBOL__';

DROP TABLE IF EXISTS stress_rank;

CREATE TABLE stress_rank AS
SELECT MAX(vol_rank) AS rank_stress, COUNT(*) AS states
FROM vol_rank;

DROP TABLE IF EXISTS regime_day;

CREATE TABLE regime_day AS
SELECT p.date AS date, v.vol_rank AS regime, p.in_sample AS in_sample
FROM hmm.hmm_prediction p
JOIN vol_rank v ON v.state = p.regime
WHERE p.symbol = '__HMM_SYMBOL__';
