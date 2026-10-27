-- A date starts a new run when its regime differs from the previous date's.
INSERT INTO hmm_rep_flag (symbol, date, regime, in_sample, new_run)
SELECT
    symbol,
    date,
    regime,
    in_sample,
    CASE WHEN regime = LAG(regime) OVER w THEN 0 ELSE 1 END
FROM model.hmm_prediction
WHERE symbol IN (__SYMBOL_LIST__)
WINDOW w AS (PARTITION BY symbol ORDER BY date);
