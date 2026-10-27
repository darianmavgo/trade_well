-- Account equity at each close: cash plus every position at that day's close. A symbol with no bars
-- that day (a delisted ticker) is valued at its last fill price, and unpriced counts how often that happened.
CREATE TABLE ibkr_position_value AS
SELECT p.date, p.symbol, p.shares,
       COALESCE(pr.close, (SELECT t.price FROM ibkr_transactions t
                           WHERE t.symbol = p.symbol AND t.date <= p.date AND t.price IS NOT NULL
                           ORDER BY t.date DESC LIMIT 1)) AS mark,
       CASE WHEN pr.close IS NULL THEN 1 ELSE 0 END AS unpriced
FROM ibkr_position_daily p
LEFT JOIN ibkr_price pr ON pr.symbol = p.symbol AND pr.date = p.date
WHERE p.shares != 0;

CREATE TABLE ibkr_equity_daily AS
SELECT c.date, c.flow, c.cash,
       COALESCE(SUM(v.shares * v.mark), 0) AS positions_value,
       c.cash + COALESCE(SUM(v.shares * v.mark), 0) AS equity,
       COALESCE(SUM(v.unpriced), 0) AS unpriced
FROM ibkr_cash_running c
LEFT JOIN ibkr_position_value v ON v.date = c.date
GROUP BY c.date, c.flow, c.cash
