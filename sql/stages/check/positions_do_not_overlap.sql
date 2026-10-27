-- Does a strategy ever hold two positions in one symbol at once? Its next entry must come on or
-- after the day the earlier trade exited. Binds: :run.
SELECT
  (SELECT count(*) FROM trades a JOIN trades b
     ON b.run_id = a.run_id AND b.strategy_id = a.strategy_id AND b.symbol = a.symbol AND b.id > a.id
    WHERE a.run_id = :run AND b.entry_date < a.exit_date AND b.exit_date > a.entry_date) AS bad,
  coalesce((SELECT group_concat(strategy_id || ' ' || symbol || ' ' || a_entry || '..' || a_exit || ' overlaps ' || b_entry, '; ') FROM (
     SELECT a.strategy_id, a.symbol, a.entry_date AS a_entry, a.exit_date AS a_exit, b.entry_date AS b_entry
     FROM trades a JOIN trades b ON b.run_id = a.run_id AND b.strategy_id = a.strategy_id AND b.symbol = a.symbol AND b.id > a.id
     WHERE a.run_id = :run AND b.entry_date < a.exit_date AND b.exit_date > a.entry_date LIMIT 5)), '') AS detail
