-- Rank the model's states by volatility: 0 calm, 1 normal, 2 stressed (the fitted regimes are
-- volatility regimes). State numbers are never used, since they follow mean return and can swap on a refit.
DROP TABLE IF EXISTS hmm_state_rank;

CREATE TABLE hmm_state_rank AS
SELECT
    symbol,
    state,
    mu_vol,
    ROW_NUMBER() OVER (ORDER BY mu_vol) - 1 AS vol_rank
FROM hmm.hmm_params
WHERE symbol = '__SYMBOL__';
