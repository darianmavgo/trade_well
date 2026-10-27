-- Trading days between the first and the last transaction, from the reference symbol's daily bars.
-- __CALENDAR_SYMBOL__ is the symbol whose bars define the trading days.
CREATE TABLE ibkr_calendar AS
SELECT DISTINCT substr(Date, 1, 10) AS date
FROM mkt.backtest_start
WHERE symbol = '__CALENDAR_SYMBOL__' AND length(Date) = 10
  AND substr(Date, 1, 10) >= (SELECT MIN(date) FROM ibkr_transactions)
  AND substr(Date, 1, 10) <= (SELECT MAX(date) FROM ibkr_transactions)
ORDER BY date;

-- Each transaction is booked on its own date, or the next trading day when it falls on a holiday or weekend.
CREATE TABLE ibkr_txn_day AS
SELECT t.rowid AS txn_id, t.type, t.symbol, t.quantity, t.net_amount,
       (SELECT MIN(c.date) FROM ibkr_calendar c WHERE c.date >= t.date) AS book_date
FROM ibkr_transactions t
WHERE t.net_amount IS NOT NULL
