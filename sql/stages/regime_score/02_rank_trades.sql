-- The same trade can sit in several report folders (a strategy re-run). Rank copies newest
-- report first, and the next stage keeps rank 1.
INSERT INTO trade_ranked (report, win, src, strategy_id, entry_date, exit_date, entry_price,
                          net_pnl, return_pct, hold_days, exit_reason, rn)
SELECT report, win, src, strategy_id, entry_date, exit_date, entry_price, net_pnl, return_pct,
       hold_days, exit_reason,
       ROW_NUMBER() OVER (PARTITION BY strategy_id, win, entry_date, exit_date, entry_price
                          ORDER BY report DESC, src)
FROM trade_all
