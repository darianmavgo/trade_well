-- Dates are cut to YYYY-MM-DD so an entry stamped with a time still joins a session date.
INSERT INTO trade_all (report, win, src, run_id, strategy_id, entry_date, exit_date, entry_price,
                       net_pnl, return_pct, hold_days, exit_reason)
SELECT __REPORT__, '__WIN__', '__SRC__', run_id, strategy_id, substr(entry_date, 1, 10),
       substr(exit_date, 1, 10), entry_price, net_pnl, return_pct, hold_days, exit_reason
FROM src.trades
