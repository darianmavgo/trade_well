INSERT INTO every_bar_signals (idx, symbol, date, open, high, low, close, volume, buylimit, entry, direction, regime, hold_days_override, take_profit, stop_loss, allocation_pct_override)
SELECT
    coalesce(idx, rowid, 0),
    symbol,
    substr(Date, 1, 10),
    open, high, low, close, volume,
    close,
    1,
    'LONG',
    'All Regimes',
    __HOLD_DAYS__,
    close * __TAKE_PROFIT_MULT__,
    close * __STOP_LOSS_MULT__,
    0.0
FROM backtest_start
WHERE symbol = '__TRADE_SYMBOL__' AND length(Date) = 10
  AND substr(Date, 1, 10) >= '__START_DATE__' AND substr(Date, 1, 10) <= '__END_DATE__'
ORDER BY Date;
