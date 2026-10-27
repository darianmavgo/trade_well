-- A symbol needs 250 bars in the window: 200 for the average and the support window.
INSERT INTO par_symbol (symbol)
SELECT symbol
FROM backtest_start
WHERE length(Date) = 10 AND substr(Date, 1, 10) >= '__START_DATE__' AND substr(Date, 1, 10) <= '__END_DATE__'
GROUP BY symbol
HAVING COUNT(*) >= 250;
