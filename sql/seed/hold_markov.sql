-- Hold GOOGL except while its Markov state is bear: sell on a bear bar, buy back
-- on the first sideways or bull bar. Needs `train markov -symbols GOOGL`.
-- Run: sqlite3 refdata/strategies.db < sql/seed/hold_markov.sql
INSERT OR REPLACE INTO hold_strategy
    (id, name, symbol, total_return, allocation_pct, cash_yield, slippage_pct, trailing_stop_pct, sma_reentry_period, regime_symbol)
VALUES
    ('googl-hold-markov', 'GOOGL Hold, Out in Markov Bear', 'GOOGL', 0, 1.0, 0.045, 0.0005, 0.0, 0, 'GOOGL');
