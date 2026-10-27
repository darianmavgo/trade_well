-- Stage 5: final verdict on a built stack (database: the in-sample stack.db).
-- Attach the held-out run as oos (data/reports/<run>/oos/stack.db). A stack
-- passes only if BOTH windows meet the goal; the oos window is the one that counts.
-- Needs `.parameter set :min_cagr 0.80` and `:max_dd 0.10`.
SELECT i.strategy_id AS stack_id,
       length(i.strategy_id) - length(replace(i.strategy_id, '+', '')) + 1 AS n_sleeves,
       round(i.cagr * 100, 1)                AS is_cagr_pct,
       round(i.max_drawdown_pct * 100, 1)    AS is_dd_pct,
       round(o.cagr * 100, 1)                AS oos_cagr_pct,
       round(o.max_drawdown_pct * 100, 1)    AS oos_dd_pct,
       o.total_trades                        AS oos_trades,
       (i.cagr >= :min_cagr AND i.max_drawdown_pct <= :max_dd
        AND o.cagr >= :min_cagr AND o.max_drawdown_pct <= :max_dd
        AND o.total_trades >= 30)            AS passes
FROM performance_summary i
JOIN oos.performance_summary o ON o.strategy_id = i.strategy_id
WHERE i.strategy_id LIKE '%+%'
ORDER BY passes DESC, o.max_drawdown_pct ASC, o.cagr DESC;
