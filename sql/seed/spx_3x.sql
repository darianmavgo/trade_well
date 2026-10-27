-- One strategy per family on the spx-3x symbol list (SPXL, UPRO).
-- Run: sqlite3 refdata/strategies.db < sql/seed/spx_3x.sql
-- markov and tree rows need: train markov SPXL UPRO ; train tree SPXL UPRO
INSERT OR REPLACE INTO hold_strategy
    (id, name, symbol, total_return, allocation_pct, cash_yield, slippage_pct, trailing_stop_pct, sma_reentry_period, regime_symbol)
VALUES
    ('spx3x-hold-spxl', 'SPXL Buy & Hold', 'SPXL', 0, 1.0, 0.045, 0.0005, 0.0, 0, ''),
    ('spx3x-hold-upro', 'UPRO Buy & Hold', 'UPRO', 0, 1.0, 0.045, 0.0005, 0.0, 0, '');

INSERT OR REPLACE INTO streak_strategy
    (id, name, signal_symbol, trade_symbol, direction, signal_days, hold_days, take_profit_pct, stop_loss_pct, regime, allocation_pct, cash_yield, slippage_pct, next_day_limit, source_strategy, source_label)
VALUES
    ('spx3x-streak-spxl-down3', 'SPXL down3 → SPXL', 'SPXL', 'SPXL', 'drop', 3, 8, 0.05, 0.0, 'All Regimes', 1.0, 0.045, 0.0005, 1, 'manual', 'spx-3x'),
    ('spx3x-streak-upro-down3', 'UPRO down3 → UPRO', 'UPRO', 'UPRO', 'drop', 3, 8, 0.05, 0.0, 'All Regimes', 1.0, 0.045, 0.0005, 1, 'manual', 'spx-3x');

INSERT OR REPLACE INTO tree_strategy
    (id, name, signal_symbol, trade_symbol, direction, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct, next_day_limit, coil_range_max, sma_bounce_min, sma_bounce_max)
VALUES
    ('spx3x_spxl_tree', 'SPXL Tree', 'SPXL', 'SPXL', 'rally', 5, 0.05, 0.05, 1.0, 0.045, 0.0005, 1, 0.05, -0.02, 0.02),
    ('spx3x_upro_tree', 'UPRO Tree', 'UPRO', 'UPRO', 'rally', 5, 0.05, 0.05, 1.0, 0.045, 0.0005, 1, 0.05, -0.02, 0.02);

INSERT OR REPLACE INTO markov_strategy
    (id, name, signal_symbol, trade_symbol, direction, target_state, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct, next_day_limit)
VALUES
    ('spx3x_markov_spxl', 'Markov SPXL', 'SPXL', 'SPXL', 'long', 'bull', 15, 0.05, 0.0, 1.0, 0.045, 0.0005, 1),
    ('spx3x_markov_upro', 'Markov UPRO', 'UPRO', 'UPRO', 'long', 'bull', 15, 0.05, 0.0, 1.0, 0.045, 0.0005, 1);

INSERT OR REPLACE INTO rotation_strategy
    (id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma, allocation_pct, cash_yield, slippage_pct, period, side, pick, entry_limit_pct, take_profit_pct, hold_days, stop_loss_pct, cooldown)
VALUES
    ('spx3x-rotation-daily-limit97', 'SPX 3x Daily 97% Limit TP3 SL5', 'spx-3x', 2, 2, 0, 0.5, 'QQQ', 0, 0.5, 0.0, 0.0005, '1d', 'long', 'all', 0.97, 0.03, 2, 0.05, 'month');

-- Gridsearch calmar #1 (1w, 90% limit, hold 2, TP 6%, SL 7%) with 100% positions.
INSERT OR REPLACE INTO rotation_strategy
    (id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma, allocation_pct, cash_yield, slippage_pct, period, side, pick, entry_limit_pct, take_profit_pct, hold_days, stop_loss_pct, cooldown)
VALUES
    ('spx3x-rotation-1w-limit90-tp6-sl7-pos100', 'SPX 3x Weekly 90% Limit TP6 SL7, 100% Positions', 'spx-3x', 2, 2, 0, 1.0, 'QQQ', 0, 1.0, 0.0, 0.0005, '1w', 'long', 'all', 0.9, 0.06, 2, 0.07, 'month');
