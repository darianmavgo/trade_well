-- Stage 2a: daily close-to-close return per symbol (database: market DB).
-- Window: bars on or before :cutoff are the only data the search may see.
DROP TABLE IF EXISTS search_02_daily_ret;
CREATE TABLE search_02_daily_ret AS
SELECT symbol,
       substr(Date, 1, 10) AS day,
       close,
       volume,
       close / LAG(close) OVER (PARTITION BY symbol ORDER BY Date) - 1 AS ret
FROM backtest_start
WHERE timeframe = '1d' AND length(Date) = 10;
