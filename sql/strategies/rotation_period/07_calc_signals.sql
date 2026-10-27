-- An entry on the entry date and an exit on the traded symbol's last session of
-- the period. A period of one session has no later session of its own, so it exits
-- on the symbol's next session. entry is 1 to open and -1 to close. Exits sort
-- before entries on the same date so a name picked twice running closes, then opens.
-- With __PERIOD_EXIT__ = 0 no exit is written: the row's hold window, take profit and
-- stop close the position.
INSERT INTO rotation_period_signals (idx, symbol, date, open, high, low, close, volume, buylimit, entry, direction, regime, hold_days_override, take_profit, stop_loss, allocation_pct_override)
SELECT idx, symbol, date, open, high, low, close, volume, close, entry, direction, 'All Regimes', 0, 0.0, 0.0, alloc
FROM (
    SELECT b.idx, b.symbol, b.date, b.open, b.high, b.low, b.close, b.volume,
           1 AS entry, CASE WHEN '__SIDE__' = 'short' THEN 'SHORT' ELSE 'LONG' END AS direction,
           __ALLOC__ AS alloc
    FROM rp_trade t
    JOIN rp_bar b ON b.symbol = t.trade_symbol AND b.date = t.entry_date
    UNION ALL
    SELECT b.idx, b.symbol, b.date, b.open, b.high, b.low, b.close, b.volume,
           -1, CASE WHEN '__SIDE__' = 'short' THEN 'SHORT' ELSE 'LONG' END, 0.0
    FROM rp_trade t
    JOIN rp_bar b ON b.symbol = t.trade_symbol AND b.period = t.period AND b.rn_last = 1
    WHERE b.date <> t.entry_date
      AND __PERIOD_EXIT__ = 1
    UNION ALL
    SELECT x.idx, x.symbol, x.date, x.open, x.high, x.low, x.close, x.volume,
           -1, CASE WHEN '__SIDE__' = 'short' THEN 'SHORT' ELSE 'LONG' END, 0.0
    FROM rp_trade t
    JOIN rp_bar b ON b.symbol = t.trade_symbol AND b.period = t.period AND b.rn_last = 1
    JOIN rp_bar x ON x.symbol = b.symbol AND x.date = b.next_date
    WHERE b.date = t.entry_date
      AND __PERIOD_EXIT__ = 1
)
ORDER BY date, entry, symbol;
