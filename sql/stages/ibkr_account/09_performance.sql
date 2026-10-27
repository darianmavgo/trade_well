-- The claimed table's timeframes, measured on the reconstructed account. A timeframe longer than the
-- account's history starts at the first day with an index, and the row says so in start_date.
CREATE TABLE ibkr_timeframe AS
SELECT 'YTD' AS timeframe, date((SELECT MAX(date) FROM ibkr_return_daily), 'start of year', '-1 day') AS wanted_start
UNION ALL SELECT '3 mo', date((SELECT MAX(date) FROM ibkr_return_daily), '-3 months')
UNION ALL SELECT '6 mo', date((SELECT MAX(date) FROM ibkr_return_daily), '-6 months')
UNION ALL SELECT '1 year', date((SELECT MAX(date) FROM ibkr_return_daily), '-12 months')
UNION ALL SELECT 'All', '0000-00-00';

ALTER TABLE ibkr_timeframe ADD COLUMN base_date TEXT;

UPDATE ibkr_timeframe SET base_date = COALESCE(
    (SELECT MAX(r.date) FROM ibkr_return_daily r WHERE r.date <= ibkr_timeframe.wanted_start AND r.idx IS NOT NULL),
    (SELECT MIN(r.date) FROM ibkr_return_daily r WHERE r.idx IS NOT NULL)
);

-- Drawdown inside each window, from the window's own running peak.
CREATE TABLE ibkr_window_dd AS
SELECT f.timeframe, r.date, r.idx,
       r.idx / MAX(r.idx) OVER (PARTITION BY f.timeframe ORDER BY r.date) - 1 AS drawdown
FROM ibkr_timeframe f
JOIN ibkr_return_daily r ON r.date >= f.base_date AND r.idx IS NOT NULL;

CREATE TABLE ibkr_actual_performance AS
SELECT f.timeframe, f.base_date AS start_date, e.date AS end_date,
       julianday(e.date) - julianday(f.base_date) AS days,
       e.idx / b.idx - 1 AS total_return,
       CASE WHEN julianday(e.date) > julianday(f.base_date)
            THEN exp(ln(e.idx / b.idx) * 365.0 / (julianday(e.date) - julianday(f.base_date))) - 1 END AS car,
       -(SELECT MIN(w.drawdown) FROM ibkr_window_dd w WHERE w.timeframe = f.timeframe) AS max_drawdown,
       (SELECT SUM(c.fills) FROM ibkr_cash_running c WHERE c.date > f.base_date) AS fills
FROM ibkr_timeframe f
JOIN ibkr_return_daily e ON e.date = (SELECT MAX(date) FROM ibkr_return_daily)
JOIN ibkr_return_daily b ON b.date = f.base_date;

ALTER TABLE ibkr_actual_performance ADD COLUMN calmar REAL;

UPDATE ibkr_actual_performance SET calmar = CASE WHEN max_drawdown > 0 THEN car / max_drawdown END
