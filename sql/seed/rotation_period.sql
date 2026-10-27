-- Period winner rows for refdata/strategies.db: the rotation family's answer to
-- biggest-winner. At the start of each calendar period hold the top_k names by the
-- previous period's return until that period's last session. Re-runnable: it
-- replaces these ids only.
--
--   sqlite3 refdata/strategies.db < sql/seed/rotation_period.sql
--
-- Needs the period and side columns, which any command that opens strategies.db
-- through pkg/refdb adds. symbols is empty: every symbol in the market database.
-- period is 1d, 1w, 1m, 1q or 1y (gridsearch sweeps it, see strategy_family_param).
-- side is long, short (cash-secured, 1% annual borrow) or inverse (the matched
-- inverse ETF of each pick).
INSERT OR REPLACE INTO rotation_strategy
    (id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma,
     allocation_pct, cash_yield, slippage_pct, period, side)
VALUES
    ('biggest-winner',         'Biggest Winner Backtest',    '', 1, 1, 0, 1.0, 'QQQ', 0, 1.0, 0, 0.0005, '1y', 'long'),
    ('biggest-winner-short',   'Biggest Winner Short',       '', 1, 1, 0, 1.0, 'QQQ', 0, 1.0, 0, 0.0005, '1y', 'short'),
    ('biggest-winner-inverse', 'Biggest Winner Inverse ETF', '', 1, 1, 0, 1.0, 'QQQ', 0, 1.0, 0, 0.0005, '1y', 'inverse');

-- rotation-2x-sector-pairs-daily-limit90: the 50 2x ETFs of symbol list
-- etf-2x-sector-pairs (25 sectors, each long and inverse). pick = all enters every
-- name each session instead of ranking them. Each name gets a buy limit at 90% of
-- the previous close that expires at the close if unfilled; a position is 10% of
-- portfolio value (allocation_pct and max_weight_pct) and nothing else caps the book.
-- Exits: take profit 5% over the fill, stop 10% under it, or the close 3 sessions after
-- the entry session. cooldown = month takes a name out of the rotation after it fills
-- until the next calendar month. Needs the cooldown column (any pkg/refdb open adds it).
INSERT OR REPLACE INTO rotation_strategy
    (id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma,
     allocation_pct, cash_yield, slippage_pct, period, side, pick,
     entry_limit_pct, take_profit_pct, hold_days, stop_loss_pct, cooldown)
VALUES
    ('rotation-2x-sector-pairs-daily-limit90', '2x Sector Pairs Daily 90% Limit', 'etf-2x-sector-pairs', 50, 50, 0, 0.10, 'QQQ', 0,
     0.10, 0, 0.0005, '1d', 'long', 'all', 0.9, 0.05, 3, 0.10, 'month');
