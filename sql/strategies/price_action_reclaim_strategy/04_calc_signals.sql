-- In an uptrend (close at or above its average), the previous close was below support and this
-- close is back above it. The stop sits just under support and a trade whose stop is further
-- from the close than the row allows is skipped. A symbol needs 250 bars in the window: 200 for
-- the average and the support window. The take-profit and hold are left 0 for the row's own.
INSERT INTO price_action_reclaim_strategy_signals (idx, symbol, date, open, high, low, close, volume, buylimit, entry, direction, regime, hold_days_override, take_profit, stop_loss, allocation_pct_override)
SELECT
    idx, symbol, date, open, high, low, close, volume,
    close,
    1,
    'LONG',
    'Price > SMA',
    0,
    0.0,
    support * __STOP_CUSHION_MULT__,
    0.0
FROM par_window
WHERE (SELECT COUNT(*) FROM par_window) >= 250
  AND rn > __SUPPORT_BARS__
  AND sma <> 0 AND close >= sma
  AND prev_close < support AND close > support
  AND (support * __STOP_CUSHION_MULT__) / close >= __MAX_STOP_RATIO__
ORDER BY date;
