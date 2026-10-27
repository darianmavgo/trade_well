-- Per strategy, window and regime, then an ALL row per strategy and window. thin marks a cell
-- under __MIN_TRADES__ trades. vs_all_avg_return_pct is the regime's mean return minus the
-- strategy's mean return over every regime in the same window.
INSERT INTO regime_score (strategy_id, win, regime, n, wins, win_rate, avg_return_pct,
                          total_net_pnl, profit_factor, avg_hold_days, thin, vs_all_avg_return_pct)
SELECT t.strategy_id, t.win, t.regime, COUNT(*), SUM(t.net_pnl > 0),
       1.0 * SUM(t.net_pnl > 0) / COUNT(*), AVG(t.return_pct), SUM(t.net_pnl),
       SUM(CASE WHEN t.net_pnl > 0 THEN t.net_pnl END) / NULLIF(-SUM(CASE WHEN t.net_pnl < 0 THEN t.net_pnl END), 0),
       AVG(t.hold_days), COUNT(*) < __MIN_TRADES__, AVG(t.return_pct) - a.avg_return_pct
FROM trade_regime t
JOIN strategy_all a ON a.strategy_id = t.strategy_id AND a.win = t.win
GROUP BY t.strategy_id, t.win, t.regime;

INSERT INTO regime_score (strategy_id, win, regime, n, wins, win_rate, avg_return_pct,
                          total_net_pnl, profit_factor, avg_hold_days, thin, vs_all_avg_return_pct)
SELECT strategy_id, win, 'ALL', COUNT(*), SUM(net_pnl > 0), 1.0 * SUM(net_pnl > 0) / COUNT(*),
       AVG(return_pct), SUM(net_pnl),
       SUM(CASE WHEN net_pnl > 0 THEN net_pnl END) / NULLIF(-SUM(CASE WHEN net_pnl < 0 THEN net_pnl END), 0),
       AVG(hold_days), COUNT(*) < __MIN_TRADES__, 0
FROM trade_regime
GROUP BY strategy_id, win
