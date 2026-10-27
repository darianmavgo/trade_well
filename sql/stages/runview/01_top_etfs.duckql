-- The largest ETFs by average daily dollar volume (close * volume) over the
-- last 90 days of daily bars. The market database is main, the universe
-- database is attached as u. __LEVERAGE__ is "" or "AND e.leverage = 'none'"
-- and __LIMIT__ is a row count.
WITH latest AS (SELECT max(substr(Date, 1, 10)) AS d FROM backtest_start WHERE length(Date) = 10)
SELECT e.symbol AS symbol, e.name AS name, e.category AS category, e.leverage AS leverage,
       avg(b.close * b.volume) AS dollar_volume, count(*) AS bars
FROM backtest_start b
JOIN u.universe e ON e.symbol = b.symbol, latest
WHERE e.is_etf = 1 AND e.asset_type <> 'CS' AND e.active = 1 __LEVERAGE__
  AND length(b.Date) = 10 AND b.Date >= date(latest.d, '-90 days')
GROUP BY e.symbol
HAVING bars >= 20
ORDER BY dollar_volume DESC
LIMIT __LIMIT__
