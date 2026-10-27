-- Window bars with their place in the symbol's period, from the first bar and from
-- the last, and the symbol's next session (where a one session period exits).
INSERT INTO rp_bar (idx, symbol, period, date, open, high, low, close, volume, rn_first, rn_last, next_date)
WITH daily AS (
    SELECT coalesce(idx, rowid, 0) AS idx, symbol, substr(Date, 1, 10) AS date,
           open, high, low, close, volume
    FROM backtest_start
    WHERE length(Date) = 10
      AND (__USE_LIST__ = 0 OR symbol IN (__SYMBOLS__))
      AND substr(Date, 1, 10) >= '__START_DATE__' AND substr(Date, 1, 10) <= '__END_DATE__'
),
keyed AS (
    SELECT daily.*, __PERIOD_KEY__ AS period FROM daily
)
SELECT
    idx, symbol, period, date, open, high, low, close, volume,
    ROW_NUMBER() OVER (PARTITION BY symbol, period ORDER BY date),
    ROW_NUMBER() OVER (PARTITION BY symbol, period ORDER BY date DESC),
    LEAD(date) OVER (PARTITION BY symbol ORDER BY date)
FROM keyed;
