-- What the account did. Needs 01 and 'attach market_history.db as m'.
.headers on
.print == names held, largest weight, invested share ==
SELECT ROUND(AVG(n), 2) AS avg_names, MIN(n) AS min_names, MAX(n) AS max_names,
       ROUND(AVG(maxw), 3) AS avg_top_weight, ROUND(MAX(maxw), 3) AS max_top_weight, ROUND(AVG(invested), 3) AS avg_invested
FROM daily;
SELECT n AS names, COUNT(*) AS days FROM daily GROUP BY n ORDER BY n;

.print == most traded symbols ==
SELECT symbol, SUM(type = 'Buy') AS buys, SUM(type = 'Sell') AS sells,
       ROUND(SUM(CASE WHEN type = 'Buy' THEN -net_amount ELSE 0 END)) AS bought
FROM tx GROUP BY symbol ORDER BY bought DESC LIMIT 25;

.print == fills by weekday (0 = Sunday) ==
SELECT strftime('%w', date) AS dow, COUNT(*) FROM tx GROUP BY 1;

.print == fill price against the day's close and open (execution is at the close) ==
SELECT COUNT(*) AS n,
       ROUND(AVG(ABS(t.price / b.close - 1)) * 100, 3) AS avg_pct_vs_close
FROM tx t JOIN m.backtest_start b
  ON b.symbol = t.symbol AND substr(b.Date, 1, 10) = t.date AND length(b.Date) = 10 AND b.timeframe = '1d'
WHERE ABS(t.price / b.close - 1) < 0.05;   -- outliers are splits in the bars, not fills
SELECT COUNT(*) AS within_1pct FROM tx t JOIN m.backtest_start b
  ON b.symbol = t.symbol AND substr(b.Date, 1, 10) = t.date AND length(b.Date) = 10 AND b.timeframe = '1d'
WHERE ABS(t.price / b.close - 1) < 0.01;

.print == holding stretches (trading sessions) ==
SELECT COUNT(*) AS stretches, ROUND(AVG(days), 1) AS avg_days, MAX(days) AS max_days FROM stretch;
SELECT CASE WHEN days <= 1 THEN '1' WHEN days <= 5 THEN '2-5' WHEN days <= 10 THEN '6-10'
            WHEN days <= 21 THEN '11-21' ELSE '>21' END AS bucket, COUNT(*) AS stretches
FROM stretch GROUP BY 1;
