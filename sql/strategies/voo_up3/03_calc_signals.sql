-- CROSS JOIN pins the loop order: signal rows outside, one index lookup per row in
-- the market bars. A plain JOIN lets SQLite loop the other way (no statistics
-- here) and re-scan the slice for every bar, which cost 90 ms a strategy.
-- BUY TQQQ when VOO has closed up __DECLINE_DAYS__ consecutive days (the
-- placeholder is reused for the up-streak window here -- see StrategyConfig.DeclineDays's
-- doc comment in pkg/strategy/strategy.go).
INSERT INTO voo_up3_signals (idx, symbol, date, open, high, low, close, volume, buylimit, entry, direction, regime, hold_days_override, take_profit, stop_loss)
SELECT
    coalesce(t.idx, t.rowid, 0) AS idx,
    'TQQQ' AS symbol,
    v.date,
    t.open,
    t.high,
    t.low,
    t.close,
    t.volume,
    t.close AS buylimit,
    1 AS entry,
    'LONG' AS direction,
    'All Regimes' AS regime,
    __HOLD_DAYS__ AS hold_days_override,
    t.close * __TAKE_PROFIT_MULT__ AS take_profit,
    t.close * __STOP_LOSS_MULT__ AS stop_loss
FROM voo_up3_streaks_slice v
CROSS JOIN backtest_start t ON t.Date = v.date AND t.symbol = 'TQQQ' AND length(t.Date) = 10
WHERE v.up_streak >= __DECLINE_DAYS__ AND t.close > 0;
