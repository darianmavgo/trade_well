-- In an uptrend (close at or above its 200-bar average), the previous close was
-- below support and this close is back above it. The stop sits just under support
-- and a trade whose stop is more than 15 percent away is skipped.
INSERT INTO price_action_reclaim_signals (idx, symbol, date, open, high, low, close, volume, buylimit, entry, direction, regime, hold_days_override, take_profit, stop_loss, allocation_pct_override)
SELECT
    idx, symbol, date, open, high, low, close, volume,
    close,
    1,
    'LONG',
    'Price > SMA200',
    0,
    0.0,
    support * 0.995,
    0.0
FROM par_window
WHERE rn > 25
  AND sma200 <> 0 AND close >= sma200
  AND prev_close < support AND close > support
  AND (support * 0.995) / close >= 0.85
ORDER BY date, symbol;
