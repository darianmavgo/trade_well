-- Did each signal buy at most once, even after its position exited? A strategy with more trades than
-- entry signals bought on a signal twice. Binds: :run.
SELECT
  (SELECT count(*) FROM (SELECT strategy_id, count(*) AS n FROM trades WHERE run_id = :run GROUP BY strategy_id) t
     JOIN (SELECT strategy_id, count(*) AS m FROM signals WHERE run_id = :run GROUP BY strategy_id) s USING (strategy_id)
     WHERE t.n > s.m) AS bad,
  coalesce((SELECT group_concat(strategy_id || ': ' || n || ' trades, ' || m || ' signals', '; ') FROM (
     SELECT t.strategy_id, t.n, s.m FROM (SELECT strategy_id, count(*) AS n FROM trades WHERE run_id = :run GROUP BY strategy_id) t
     JOIN (SELECT strategy_id, count(*) AS m FROM signals WHERE run_id = :run GROUP BY strategy_id) s USING (strategy_id)
     WHERE t.n > s.m LIMIT 5)), '') AS detail
