-- 50- and 200-bar simple moving averages of close (view of sql/stages/bar_sma).
-- A short history still yields the average of the bars that exist.
DROP VIEW IF EXISTS v_bar_sma;
CREATE VIEW v_bar_sma AS
SELECT
    symbol,
    date,
    AVG(close) OVER (PARTITION BY symbol ORDER BY date ROWS BETWEEN 49 PRECEDING AND CURRENT ROW) AS sma50,
    AVG(close) OVER (PARTITION BY symbol ORDER BY date ROWS BETWEEN 199 PRECEDING AND CURRENT ROW) AS sma200
FROM v_daily_bars;
