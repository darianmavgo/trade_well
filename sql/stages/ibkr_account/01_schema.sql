-- Slice tables of one account reconstruction. ibkr_transactions holds the statement rows,
-- claimed_performance the claimed table, and mkt is the attached market database.
-- Every table below is rebuilt on each run, in stage order, so any step can be inspected with a SELECT.
DROP TABLE IF EXISTS ibkr_calendar;
DROP TABLE IF EXISTS ibkr_txn_day;
DROP TABLE IF EXISTS ibkr_cash_daily;
DROP TABLE IF EXISTS ibkr_cash_running;
DROP TABLE IF EXISTS ibkr_fill_factor;
DROP TABLE IF EXISTS ibkr_qty_daily;
DROP TABLE IF EXISTS ibkr_position_daily;
DROP TABLE IF EXISTS ibkr_price;
DROP TABLE IF EXISTS ibkr_position_value;
DROP TABLE IF EXISTS ibkr_equity_daily;
DROP TABLE IF EXISTS ibkr_return_step;
DROP TABLE IF EXISTS ibkr_return_daily;
DROP TABLE IF EXISTS ibkr_timeframe;
DROP TABLE IF EXISTS ibkr_window_dd;
DROP TABLE IF EXISTS ibkr_price_check;
DROP TABLE IF EXISTS ibkr_claim_vs_actual;
DROP TABLE IF EXISTS ibkr_actual_performance
