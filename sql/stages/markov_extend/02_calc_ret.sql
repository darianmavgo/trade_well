-- 20-bar return of the close, as in markov_train. __THROUGH__ ('' = no limit) is the last
-- completed session: a bar after it may still be forming and must not enter the model.
INSERT INTO markov_x_ret (symbol, date, close, ret_20d)
SELECT
    symbol,
    substr(Date, 1, 10),
    close,
    (close - LAG(close, 20) OVER w) / LAG(close, 20) OVER w
FROM backtest_start
WHERE timeframe = '1d' AND symbol IN (__SYMBOL_LIST__)
  AND ('__THROUGH__' = '' OR substr(Date, 1, 10) <= '__THROUGH__')
WINDOW w AS (PARTITION BY symbol ORDER BY Date)
