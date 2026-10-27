-- CROSS JOIN pins the loop order: signal rows outside, one index lookup per row in
-- the market bars. A plain JOIN lets SQLite loop the other way (no statistics
-- here) and re-scan the slice for every bar, which cost 90 ms a strategy.
INSERT INTO markov_model_signals (idx, symbol, date, open, high, low, close, volume, buylimit, entry, direction, regime, hold_days_override, take_profit, stop_loss, allocation_pct_override)
SELECT
    coalesce(t.idx, t.rowid, 0) AS idx,
    '__TRADE_SYMBOL__' AS symbol,
    p.Date,
    t.open,
    t.high,
    t.low,
    t.close,
    t.volume,
    t.close AS buylimit,
    1 AS entry,
    '__DIRECTION__' AS direction,
    CASE 
        WHEN p.current_state = 1 THEN 'bull'
        WHEN p.current_state = -1 THEN 'bear'
        ELSE 'sideways'
    END AS regime,
    __HOLD_DAYS__ AS hold_days_override,
    t.close * __TAKE_PROFIT_MULT__ AS take_profit,
    t.close * __STOP_LOSS_MULT__ AS stop_loss,
    CASE 
        WHEN abs(p.signal) >= 0.50 THEN 0.30
        WHEN abs(p.signal) >= 0.30 THEN 0.20
        WHEN abs(p.signal) >= 0.10 THEN 0.10
        ELSE 0.05
    END AS allocation_pct_override
FROM markov_model_predictions p
CROSS JOIN market.backtest_start t ON t.Date = p.Date AND t.symbol = '__TRADE_SYMBOL__' AND length(t.Date) = 10
WHERE p.symbol = '__SYMBOL__' 
  AND CASE 
        WHEN p.current_state = 1 THEN 'bull'
        WHEN p.current_state = -1 THEN 'bear'
        ELSE 'sideways'
      END = lower('__REGIME_LABEL__')
  AND (
        (lower('__REGIME_LABEL__') = 'bull' AND p.signal > 0.10) OR
        (lower('__REGIME_LABEL__') = 'bear' AND p.signal < -0.10) OR
        (lower('__REGIME_LABEL__') = 'sideways' AND abs(p.signal) <= 0.10)
      );
