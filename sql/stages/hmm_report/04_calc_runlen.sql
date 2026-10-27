-- A run that crosses the in-sample boundary is counted once per side.
INSERT INTO hmm_rep_runlen (symbol, regime, in_sample, run_id, len)
SELECT symbol, regime, in_sample, run_id, COUNT(*)
FROM hmm_rep_run
GROUP BY symbol, regime, in_sample, run_id;
