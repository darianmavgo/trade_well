-- Slice table: the last __DAYS__ daily bars of every symbol, newest first (rn = 1 is the latest bar).
-- mkt is the attached market database. Minute bars share the table and are skipped by length(Date) = 10.
DROP TABLE IF EXISTS avgvol_window_bars;

CREATE TABLE avgvol_window_bars AS
SELECT symbol, date, volume, rn
FROM (
    SELECT symbol,
           substr(Date, 1, 10) AS date,
           volume,
           ROW_NUMBER() OVER (PARTITION BY symbol ORDER BY Date DESC) AS rn
    FROM mkt.backtest_start
    WHERE length(Date) = 10 AND volume IS NOT NULL
)
WHERE rn <= __DAYS__
