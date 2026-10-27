-- 20-session rolling returns. The market_context study builds `ohlcv`
-- (ticker, date, close) from daily backtest_start bars before running this.
SELECT
    ticker,
    date,
    close,
    -- 20-Day Simple Return: (Close - Close_20_Days_Ago) / Close_20_Days_Ago
    (close - LAG(close, 20) OVER (PARTITION BY ticker ORDER BY date)) /
        LAG(close, 20) OVER (PARTITION BY ticker ORDER BY date) AS return_20d_simple,

    -- 20-Day Log Return: ln(Close / Close_20_Days_Ago)
    LN(close / LAG(close, 20) OVER (PARTITION BY ticker ORDER BY date)) AS return_20d_log
FROM ohlcv
WHERE ticker IN ('VOO', 'IEF', 'GLD', 'USO', 'HYG')
ORDER BY ticker, date ASC;
