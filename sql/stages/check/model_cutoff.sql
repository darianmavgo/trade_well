-- Did every Markov model learn only from the in-sample period? trained_through must be recorded and
-- not later than :through (a blank :through checks only that it is recorded). Read-only on the model database.
SELECT
  (SELECT count(*) FROM markov_model_meta WHERE trained_through IS NULL OR trained_through = ''
      OR (:through <> '' AND trained_through > :through)) AS bad,
  coalesce((SELECT group_concat(symbol || ' ' || coalesce(trained_through, 'none'), '; ') FROM (
      SELECT symbol, trained_through FROM markov_model_meta WHERE trained_through IS NULL OR trained_through = ''
        OR (:through <> '' AND trained_through > :through) LIMIT 5)), '') AS detail
