-- Close of every traded symbol on every trading day, from the market database.
-- Option contracts (a symbol with a space) are not in it and are left out.
CREATE TABLE ibkr_price AS
SELECT symbol, substr(Date, 1, 10) AS date, close
FROM mkt.backtest_start
WHERE length(Date) = 10
  AND symbol IN (SELECT DISTINCT symbol FROM ibkr_txn_day WHERE type IN ('Buy', 'Sell') AND symbol NOT LIKE '% %')
  AND substr(Date, 1, 10) >= (SELECT MIN(date) FROM ibkr_calendar)
  AND substr(Date, 1, 10) <= (SELECT MAX(date) FROM ibkr_calendar);

-- Each fill's price against that day's close. A ratio far from 1 means the bars are on a different
-- split basis than the statement, which the next stage corrects.
CREATE TABLE ibkr_price_check AS
SELECT t.rowid AS txn_id, t.date, t.symbol, t.price, p.close, t.price / p.close AS ratio
FROM ibkr_transactions t
JOIN ibkr_price p ON p.symbol = t.symbol AND p.date = t.date
WHERE t.type IN ('Buy', 'Sell') AND t.price IS NOT NULL AND p.close > 0
