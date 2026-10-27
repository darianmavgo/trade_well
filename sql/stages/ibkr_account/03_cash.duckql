-- External flows (deposits and withdrawals) are separated from every other cash movement
-- (trades, dividends, interest, fees). Cash is the running total of both.
CREATE TABLE ibkr_cash_daily AS
SELECT c.date,
       COALESCE(SUM(CASE WHEN d.type IN ('Deposit', 'Withdrawal') THEN d.net_amount END), 0) AS flow,
       COALESCE(SUM(CASE WHEN d.type NOT IN ('Deposit', 'Withdrawal') THEN d.net_amount END), 0) AS trading_cash,
       COALESCE(SUM(CASE WHEN d.type IN ('Buy', 'Sell') THEN 1 END), 0) AS fills
FROM ibkr_calendar c
LEFT JOIN ibkr_txn_day d ON d.book_date = c.date
GROUP BY c.date;

CREATE TABLE ibkr_cash_running AS
SELECT date, flow, trading_cash, fills,
       SUM(flow + trading_cash) OVER (ORDER BY date) AS cash
FROM ibkr_cash_daily
