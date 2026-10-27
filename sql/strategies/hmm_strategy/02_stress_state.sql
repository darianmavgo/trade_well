-- The model is trained by train hmm and saved in the HMM models database. Backtests only read it.
-- States are numbered by mean return, which is not stable between refits (the fitted regimes are
-- volatility regimes), so the stressed state is found by volatility: the state with the highest
-- mean realized volatility. No state number is written anywhere in this pipeline.
ATTACH DATABASE '__HMM_MODEL_DB__' AS hmm;

DROP TABLE IF EXISTS hmm_stress_state;

CREATE TABLE hmm_stress_state AS
SELECT symbol, state, mu_vol
FROM hmm.hmm_params
WHERE symbol = '__SYMBOL__'
ORDER BY mu_vol DESC
LIMIT 1;
