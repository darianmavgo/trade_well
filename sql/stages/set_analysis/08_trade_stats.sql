-- Trades as the shared account took them, per strategy and for the set. A win is a positive net P&L.
DROP TABLE IF EXISTS set_strategy_stats;

CREATE TABLE set_strategy_stats AS
SELECT
    strategy_id,
    COUNT(*) AS trades,
    SUM(CASE WHEN net_pnl > 0 THEN 1 ELSE 0 END) AS wins,
    SUM(CASE WHEN net_pnl > 0 THEN 1 ELSE 0 END) * 1.0 / COUNT(*) AS win_rate,
    AVG(return_pct) AS avg_return_pct,
    MIN(return_pct) AS worst_return_pct,
    AVG(hold_days) AS avg_hold_days,
    SUM(net_pnl) AS net_pnl
FROM set_trade
GROUP BY strategy_id;

DROP TABLE IF EXISTS set_trade_summary;

CREATE TABLE set_trade_summary AS
SELECT
    COUNT(*) AS trades,
    SUM(CASE WHEN net_pnl > 0 THEN 1 ELSE 0 END) AS wins,
    SUM(CASE WHEN net_pnl > 0 THEN 1 ELSE 0 END) * 1.0 / MAX(1, COUNT(*)) AS win_rate,
    AVG(return_pct) AS avg_return_pct,
    MIN(return_pct) AS worst_return_pct,
    SUM(net_pnl) AS net_pnl
FROM set_trade
