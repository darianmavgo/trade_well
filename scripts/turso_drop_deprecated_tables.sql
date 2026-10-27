-- Drops the Turso tables that no code touches any more.
--
-- Group 1 (nothing reads or writes them):
--   _litestream_lock, _litestream_seq   Litestream bookkeeping (Litestream was removed)
--   audit_slippage                      old name of live_vs_backtest_slippage (0 rows)
--   reconcile_trade                     old name of live_vs_backtest_trade (0 rows)
--   backtest_winrate                    stale duplicate of live_vs_backtest_winrate (4 rows)
-- Group 2 (their creators were deleted; nothing ever wrote to them):
--   dashboard, runtimeenv, account_allowlist
-- Group 3 (plain-named Schwab mirror, duplicated by the account_state_* tables):
--   accounts, positions, orders, order_legs, transactions, raw_responses, user_preferences
--   The one user_preferences row (unused streamer config) is lost.
--
-- RUN THIS ONLY AFTER the new code is deployed. The old deployed app recreates groups 2 and 3
-- with CREATE TABLE IF NOT EXISTS (and `schwaber listall` from an old build refills group 3).
--
-- Run it yourself, for example with the Turso CLI:
--   turso db shell <your-database> < scripts/turso_drop_deprecated_tables.sql
-- DROP TABLE cannot be undone. Read the checks below first. The script is safe to run twice only
-- if you skip the checks (they fail with "no such table" once the tables are gone); the DROPs are IF EXISTS.

-- CHECK 1: must print 0 (strategies in backtest_winrate missing from live_vs_backtest_winrate).
SELECT COUNT(*) AS group1_rows_only_in_old_table
FROM backtest_winrate
WHERE strategy_id NOT IN (SELECT strategy_id FROM live_vs_backtest_winrate);

-- CHECK 2: every one of these must print 0 (rows in a plain mirror table with no twin in account_state_*).
SELECT COUNT(*) AS accounts_only_in_plain FROM accounts
WHERE account_number NOT IN (SELECT account_number FROM account_state_account);
SELECT COUNT(*) AS orders_only_in_plain FROM orders
WHERE order_id NOT IN (SELECT order_id FROM account_state_order);
SELECT COUNT(*) AS transactions_only_in_plain FROM transactions
WHERE activity_id NOT IN (SELECT activity_id FROM account_state_transaction);
SELECT COUNT(*) AS positions_only_in_plain FROM positions
WHERE (account_number, symbol) NOT IN (SELECT account_number, symbol FROM account_state_position);

-- CHECK 3: dashboard, runtimeenv, account_allowlist should hold nothing you need.
SELECT 'dashboard' AS t, COUNT(*) AS n FROM dashboard
UNION ALL SELECT 'runtimeenv', COUNT(*) FROM runtimeenv
UNION ALL SELECT 'account_allowlist', COUNT(*) FROM account_allowlist;

-- Group 1
DROP TABLE IF EXISTS "_litestream_lock";
DROP TABLE IF EXISTS "_litestream_seq";
DROP TABLE IF EXISTS audit_slippage;
DROP TABLE IF EXISTS reconcile_trade;
DROP TABLE IF EXISTS backtest_winrate;
-- Group 2
DROP TABLE IF EXISTS dashboard;
DROP TABLE IF EXISTS runtimeenv;
DROP TABLE IF EXISTS account_allowlist;
-- Group 3
DROP TABLE IF EXISTS order_legs;
DROP TABLE IF EXISTS orders;
DROP TABLE IF EXISTS positions;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS user_preferences;
DROP TABLE IF EXISTS raw_responses;
DROP TABLE IF EXISTS accounts;

-- AFTER: must list nothing, and the live tables must still have their rows.
SELECT name AS still_there FROM sqlite_master WHERE name IN (
  '_litestream_lock', '_litestream_seq', 'audit_slippage', 'reconcile_trade', 'backtest_winrate',
  'dashboard', 'runtimeenv', 'account_allowlist',
  'accounts', 'positions', 'orders', 'order_legs', 'transactions', 'raw_responses', 'user_preferences');
SELECT COUNT(*) AS live_vs_backtest_winrate_rows FROM live_vs_backtest_winrate;
SELECT COUNT(*) AS account_state_order_rows FROM account_state_order;
