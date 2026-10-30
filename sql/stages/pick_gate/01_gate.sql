-- Appends the strategies of source __SRC__ whose win rate and trade count pass in its in-sample database (__INS__) and its held-out database (__OOS__). Both are attached by runner.PickSet.
INSERT INTO pick_candidate
SELECT
    __SRC__,
    i.strategy_id,
    i.win_rate,
    i.total_trades,
    i.cagr,
    i.max_drawdown_pct,
    o.win_rate,
    o.total_trades,
    o.cagr,
    o.max_drawdown_pct,
    MIN(i.win_rate, o.win_rate)
FROM __INS__.performance_summary i
JOIN __OOS__.performance_summary o ON o.strategy_id = i.strategy_id
WHERE i.win_rate >= __MIN_WIN__ AND o.win_rate >= __MIN_WIN__
  AND i.total_trades >= __MIN_TRADES__ AND o.total_trades >= __MIN_OOS_TRADES__
