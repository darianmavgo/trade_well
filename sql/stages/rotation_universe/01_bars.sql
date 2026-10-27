-- Rotation universe, point in time. Reads the daily bars of the market database (attached as m)
-- and writes slice tables into a fresh calculation database. Each file reads the tables of the
-- ones before it. b0 is every daily bar of a real ticker: no options, no numbered series.
DROP TABLE IF EXISTS b0;
CREATE TABLE b0 AS
SELECT symbol, substr(Date, 1, 10) AS date, open, high, low, close, volume
FROM m.backtest_start
WHERE length(Date) = 10 AND timeframe = '1d' AND close > 0 AND volume > 0
  AND symbol NOT LIKE '% %' AND symbol NOT GLOB '*[0-9]*';
CREATE INDEX b0_sd ON b0 (symbol, date);
