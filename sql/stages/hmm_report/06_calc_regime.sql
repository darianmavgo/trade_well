INSERT INTO hmm_rep_regime (symbol, regime, in_sample, days, mean_ret, mean_vol, mean_loglik, avg_run)
SELECT
    p.symbol,
    p.regime,
    p.in_sample,
    COUNT(*),
    AVG(p.ret),
    AVG(p.vol),
    AVG(p.loglik_bar),
    a.avg_run
FROM model.hmm_prediction p
JOIN hmm_rep_avgrun a
  ON a.symbol = p.symbol AND a.regime = p.regime AND a.in_sample = p.in_sample
WHERE p.symbol IN (__SYMBOL_LIST__)
GROUP BY p.symbol, p.regime, p.in_sample, a.avg_run;
