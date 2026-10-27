INSERT INTO hmm_rep_run (symbol, date, regime, in_sample, run_id)
SELECT
    symbol,
    date,
    regime,
    in_sample,
    SUM(new_run) OVER (PARTITION BY symbol ORDER BY date)
FROM hmm_rep_flag;
