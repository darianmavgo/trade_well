INSERT INTO trade_dedup (report, win, src, strategy_id, entry_date, exit_date, entry_price,
                         net_pnl, return_pct, hold_days, exit_reason)
SELECT report, win, src, strategy_id, entry_date, exit_date, entry_price, net_pnl, return_pct,
       hold_days, exit_reason
FROM trade_ranked
WHERE rn = 1
