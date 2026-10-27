-- Daily session bars of every symbol. Hourly bars share backtest_start and are skipped by length(Date) = 10.
DROP VIEW IF EXISTS v_daily_bars;
CREATE VIEW v_daily_bars AS
SELECT
    coalesce(idx, rowid, 0) AS idx,
    symbol,
    substr(Date, 1, 10) AS date,
    open, high, low, close, volume
FROM backtest_start
WHERE length(Date) = 10;
