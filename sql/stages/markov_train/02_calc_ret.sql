-- 20-bar return of the close. __SYMBOL_LIST__ is the batch's quoted symbols.
INSERT INTO markov_ret (symbol, date, close, ret_20d)
SELECT
    symbol,
    substr(Date, 1, 10),
    close,
    (close - LAG(close, 20) OVER w) / LAG(close, 20) OVER w
FROM backtest_start
WHERE timeframe = '1d' AND symbol IN (__SYMBOL_LIST__)
WINDOW w AS (PARTITION BY symbol ORDER BY Date);
