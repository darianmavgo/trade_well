-- Does every trade exit on or after its entry, with a positive size and positive prices? Binds: :run.
SELECT
  (SELECT count(*) FROM trades WHERE run_id = :run
     AND (exit_date < entry_date OR shares <= 0 OR entry_price <= 0 OR exit_price <= 0)) AS bad,
  coalesce((SELECT group_concat(strategy_id || ' ' || symbol || ' ' || entry_date || '->' || exit_date, '; ') FROM (
     SELECT strategy_id, symbol, entry_date, exit_date FROM trades WHERE run_id = :run
       AND (exit_date < entry_date OR shares <= 0 OR entry_price <= 0 OR exit_price <= 0) LIMIT 5)), '') AS detail
