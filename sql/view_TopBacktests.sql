CREATE VIEW "TopPerformers" AS SELECT
  strategy_id, max_drawdown_pct, cagr, total_trades, win_rate
FROM
  "performance_summary"
where total_trades > 30
and max_drawdown_pct < 0.3
and cagr > 0.4
ORDER BY
  win_rate DESC;
