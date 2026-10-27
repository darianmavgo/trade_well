-- Regime of a trade = state of the last session strictly before its entry date. A trade before
-- the first session has no match and is labelled unlabeled.
INSERT INTO trade_regime (report, win, src, strategy_id, entry_date, exit_date, entry_price,
                          net_pnl, return_pct, hold_days, exit_reason, regime_date, regime)
SELECT t.report, t.win, t.src, t.strategy_id, t.entry_date, t.exit_date, t.entry_price,
       t.net_pnl, t.return_pct, t.hold_days, t.exit_reason, s.date,
       CASE s.state WHEN 1 THEN 'bull' WHEN -1 THEN 'bear' WHEN 0 THEN 'sideways' ELSE 'unlabeled' END
FROM trade_dedup t
LEFT JOIN spy_session s ON t.entry_date > s.date AND t.entry_date <= s.next_date
