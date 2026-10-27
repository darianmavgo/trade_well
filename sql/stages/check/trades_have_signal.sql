-- Did every trade start from a signal? A trade needs a signal of its strategy on its symbol dated
-- no later than its entry and within the 7 days before it (the entry may be a session or two after
-- the signal). Binds: :run.
SELECT
  (SELECT count(*) FROM trades t WHERE t.run_id = :run AND NOT EXISTS (
     SELECT 1 FROM signals s WHERE s.run_id = t.run_id AND s.strategy_id = t.strategy_id AND s.symbol = t.symbol
       AND s.date <= t.entry_date AND s.date >= date(t.entry_date, '-7 day'))) AS bad,
  coalesce((SELECT group_concat(strategy_id || ' ' || symbol || ' ' || entry_date, '; ') FROM (
     SELECT t.strategy_id, t.symbol, t.entry_date FROM trades t WHERE t.run_id = :run AND NOT EXISTS (
       SELECT 1 FROM signals s WHERE s.run_id = t.run_id AND s.strategy_id = t.strategy_id AND s.symbol = t.symbol
         AND s.date <= t.entry_date AND s.date >= date(t.entry_date, '-7 day')) LIMIT 5)), '') AS detail
