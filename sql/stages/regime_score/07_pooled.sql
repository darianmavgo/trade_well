-- Every strategy together, by window and regime. Trades are equally weighted, so a strategy
-- that trades often counts more.
INSERT INTO regime_pooled (win, regime, strategies, n, wins, win_rate, avg_return_pct,
                           total_net_pnl, profit_factor, avg_hold_days, thin)
SELECT win, regime, COUNT(DISTINCT strategy_id), COUNT(*), SUM(net_pnl > 0),
       1.0 * SUM(net_pnl > 0) / COUNT(*), AVG(return_pct), SUM(net_pnl),
       SUM(CASE WHEN net_pnl > 0 THEN net_pnl END) / NULLIF(-SUM(CASE WHEN net_pnl < 0 THEN net_pnl END), 0),
       AVG(hold_days), COUNT(*) < __MIN_TRADES__
FROM trade_regime
GROUP BY win, regime;

INSERT INTO regime_pooled (win, regime, strategies, n, wins, win_rate, avg_return_pct,
                           total_net_pnl, profit_factor, avg_hold_days, thin)
SELECT win, 'ALL', COUNT(DISTINCT strategy_id), COUNT(*), SUM(net_pnl > 0),
       1.0 * SUM(net_pnl > 0) / COUNT(*), AVG(return_pct), SUM(net_pnl),
       SUM(CASE WHEN net_pnl > 0 THEN net_pnl END) / NULLIF(-SUM(CASE WHEN net_pnl < 0 THEN net_pnl END), 0),
       AVG(hold_days), COUNT(*) < __MIN_TRADES__
FROM trade_regime
GROUP BY win
