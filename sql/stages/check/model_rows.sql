-- Does every model have predictions, and none past its last date? (A delisted symbol's last date
-- is before the global training cutoff, which is fine.)
SELECT
  (SELECT count(*) FROM markov_model_meta m WHERE NOT EXISTS (SELECT 1 FROM markov_prediction p WHERE p.symbol = m.symbol)
      OR EXISTS (SELECT 1 FROM markov_prediction p WHERE p.symbol = m.symbol AND p.date > m.last_date)) AS bad,
  coalesce((SELECT group_concat(symbol, ', ') FROM (SELECT m.symbol FROM markov_model_meta m
      WHERE NOT EXISTS (SELECT 1 FROM markov_prediction p WHERE p.symbol = m.symbol)
         OR EXISTS (SELECT 1 FROM markov_prediction p WHERE p.symbol = m.symbol AND p.date > m.last_date)  LIMIT 5)), '') AS detail
