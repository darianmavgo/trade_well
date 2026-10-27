-- Entry = 1 on the session a name becomes held, entry = -1 on the session it
-- stops being held (the simulator closes that position). Nothing else sells: the
-- ranking is the only exit, as in the Omnifunds account.
INSERT INTO rotation_strategy_signals (idx, symbol, date, open, high, low, close, volume, buylimit, entry, direction, regime, hold_days_override, take_profit, stop_loss, allocation_pct_override)
SELECT
    f.idx, f.symbol, f.date, f.open, f.high, f.low, f.close, f.volume,
    f.close,
    CASE WHEN s.held = 1 THEN 1 ELSE -1 END,
    'LONG',
    CASE WHEN __REGIME_ON__ = 1 THEN '__REGIME_SYMBOL__>SMA__REGIME_SMA__' ELSE 'All Regimes' END,
    0,
    0.0,
    0.0,
    CASE WHEN s.held = 1 THEN __ALLOC__ ELSE 0.0 END
FROM rot_state s
JOIN rot_feature f ON f.symbol = s.symbol AND f.date = s.date
WHERE s.held <> s.prev_held
  AND s.date >= '__START_DATE__' AND s.date <= '__END_DATE__'
ORDER BY f.date, f.symbol;
