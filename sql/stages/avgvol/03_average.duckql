-- Average daily share volume over the window. A symbol with fewer than __DAYS__ bars is averaged over the bars it has, and bars_used says how many.
DELETE FROM avg_volume WHERE window_days = __DAYS__;

INSERT INTO avg_volume (symbol, window_days, avg_volume, bars_used, first_date, last_date)
SELECT symbol, __DAYS__, AVG(volume), COUNT(*), MIN(date), MAX(date)
FROM avgvol_window_bars
GROUP BY symbol
