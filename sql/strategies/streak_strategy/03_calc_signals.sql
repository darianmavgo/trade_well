-- CROSS JOIN pins the loop order: signal rows outside, one index lookup per row in
-- the market bars. A plain JOIN lets SQLite loop the other way (no statistics
-- here) and re-scan the slice for every bar, which cost 90 ms a strategy.
-- Buy the trade symbol when the watch symbol's streak is long enough.
-- __STREAK_COL__ is down_streak or up_streak. __REGIME_PREDICATE__ is
-- applied to the watch bar (alias v), not the trade bar.
-- take_profit and stop_loss are 0 on purpose: the simulator then measures the
-- row's take_profit_pct and stop_loss_pct from the real entry price (the next
-- session's open), not from this bar's close. A row with no stop gets the
-- simulator's crisis stop.
INSERT INTO streak_strategy_signals (
    idx, symbol, date, open, high, low, close, volume, buylimit, entry,
    direction, regime, hold_days_override, take_profit, stop_loss
)
SELECT
    coalesce(t.idx, t.rowid, 0) AS idx,
    '__TRADE_SYMBOL__' AS symbol,
    v.date,
    t.open,
    t.high,
    t.low,
    t.close,
    t.volume,
    t.close AS buylimit,
    1 AS entry,
    'LONG' AS direction,
    '__REGIME_LABEL__' AS regime,
    __HOLD_DAYS__ AS hold_days_override,
    0.0 AS take_profit,
    0.0 AS stop_loss
FROM streak_strategy_slice v
CROSS JOIN backtest_start t
  ON t.Date = v.date
 AND t.symbol = '__TRADE_SYMBOL__'
 AND length(t.Date) = 10
WHERE v.__STREAK_COL__ >= __DECLINE_DAYS__
  AND (__REGIME_PREDICATE__)
  AND t.close > 0;
