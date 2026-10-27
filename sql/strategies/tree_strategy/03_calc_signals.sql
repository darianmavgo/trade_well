-- CROSS JOIN pins the loop order: signal rows outside, one index lookup per row in
-- the market bars. A plain JOIN lets SQLite loop the other way (no statistics
-- here) and re-scan the slice for every bar, which cost 90 ms a strategy.
-- Buy the trade symbol on every bar the tree predicts class 2 (the next bar up 5
-- percent or more).
INSERT INTO tree_strategy_signals (idx, symbol, date, open, high, low, close, volume, buylimit, entry, direction, regime, hold_days_override, take_profit, stop_loss, allocation_pct_override)
SELECT
    coalesce(t.idx, t.rowid, 0),
    '__TRADE_SYMBOL__',
    l.date,
    t.open, t.high, t.low, t.close, t.volume,
    t.close,
    1,
    'LONG',
    'Tree class 2',
    __HOLD_DAYS__,
    t.close * __TAKE_PROFIT_MULT__,
    t.close * __STOP_LOSS_MULT__,
    0.0
FROM tree_leaf l
CROSS JOIN backtest_start t ON t.Date = l.date AND t.symbol = '__TRADE_SYMBOL__' AND length(t.Date) = 10
WHERE l.pred = '2' AND t.close > 0
ORDER BY l.date;
