INSERT INTO hmm_rep_avgrun (symbol, regime, in_sample, avg_run)
SELECT symbol, regime, in_sample, AVG(len)
FROM hmm_rep_runlen
GROUP BY symbol, regime, in_sample;
