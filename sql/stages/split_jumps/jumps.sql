-- Every close-to-close move between two consecutive sessions of a symbol beyond __RATIO__x (or
-- under its inverse). In this data that is almost always an unadjusted split, not price action,
-- and a strategy that trades through one books a gain or loss nobody could have had.
-- Read-only query. __SYMBOLS__ is a quoted list, __START__ and __END__ bound the sessions reported.
SELECT symbol, date, ratio
FROM (
    SELECT symbol,
           substr(Date, 1, 10) AS date,
           close / lag(close) OVER (PARTITION BY symbol ORDER BY Date) AS ratio
    FROM backtest_start
    WHERE length(Date) = 10 AND close > 0 AND symbol IN (__SYMBOLS__)
)
WHERE date >= '__START__' AND date <= '__END__'
  AND (ratio > __RATIO__ OR ratio < 1.0 / __RATIO__)
ORDER BY symbol, date
