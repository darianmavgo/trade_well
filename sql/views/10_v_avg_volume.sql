-- Average daily share volume over the last 20, 50 and 200 sessions (view of sql/stages/avgvol).
-- A symbol with fewer bars is averaged over the bars it has, and bars_used says how many.
DROP VIEW IF EXISTS v_avg_volume_rank;
CREATE VIEW v_avg_volume_rank AS
SELECT symbol, date, volume,
       ROW_NUMBER() OVER (PARTITION BY symbol ORDER BY date DESC) AS rn
FROM v_daily_bars
WHERE volume IS NOT NULL;

DROP VIEW IF EXISTS v_avg_volume;
CREATE VIEW v_avg_volume AS
SELECT w.window_days, r.symbol, AVG(r.volume) AS avg_volume, COUNT(*) AS bars_used,
       MIN(r.date) AS first_date, MAX(r.date) AS last_date
FROM (SELECT 20 AS window_days UNION ALL SELECT 50 UNION ALL SELECT 200) w
JOIN v_avg_volume_rank r ON r.rn <= w.window_days
GROUP BY w.window_days, r.symbol;
