-- __SYMBOL_LIST__ is a quoted, comma-separated list of the symbols to average.
INSERT INTO bar_sma (symbol, date, sma50, sma200)
SELECT
    symbol,
    substr(Date, 1, 10) AS date,
    AVG(close) OVER (PARTITION BY symbol ORDER BY substr(Date, 1, 10) ROWS BETWEEN 49 PRECEDING AND CURRENT ROW) AS sma50,
    AVG(close) OVER (PARTITION BY symbol ORDER BY substr(Date, 1, 10) ROWS BETWEEN 199 PRECEDING AND CURRENT ROW) AS sma200
FROM backtest_start
WHERE length(Date) = 10 AND symbol IN (__SYMBOL_LIST__);
