-- Shares bought or sold per symbol per trading day on the bars' basis, then the shares held at each close.
-- A fill with no price check (no bars that day) keeps factor 1.
CREATE TABLE ibkr_qty_daily AS
SELECT d.book_date AS date, d.symbol, SUM(d.quantity * COALESCE(f.factor, 1.0)) AS qty
FROM ibkr_txn_day d
LEFT JOIN ibkr_fill_factor f ON f.txn_id = d.txn_id
WHERE d.type IN ('Buy', 'Sell') AND d.quantity IS NOT NULL AND d.symbol NOT LIKE '% %'
GROUP BY d.book_date, d.symbol;

CREATE TABLE ibkr_position_daily AS
SELECT c.date, s.symbol,
       SUM(COALESCE(q.qty, 0)) OVER (PARTITION BY s.symbol ORDER BY c.date) AS shares
FROM ibkr_calendar c
CROSS JOIN (SELECT DISTINCT symbol FROM ibkr_qty_daily) s
LEFT JOIN ibkr_qty_daily q ON q.date = c.date AND q.symbol = s.symbol
