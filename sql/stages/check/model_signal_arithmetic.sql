-- Is a model's signal always prob_bull minus prob_bear, and every probability a probability?
SELECT
  (SELECT count(*) FROM markov_prediction WHERE abs(signal - (prob_bull - prob_bear)) > 1e-6
      OR prob_bull < 0 OR prob_bear < 0 OR prob_bull > 1 OR prob_bear > 1 OR prob_bull + prob_bear > 1 + 1e-6) AS bad,
  coalesce((SELECT group_concat(symbol || ' ' || date, '; ') FROM (
      SELECT symbol, date FROM markov_prediction WHERE abs(signal - (prob_bull - prob_bear)) > 1e-6
        OR prob_bull < 0 OR prob_bear < 0 OR prob_bull > 1 OR prob_bear > 1 OR prob_bull + prob_bear > 1 + 1e-6 LIMIT 5)), '') AS detail
