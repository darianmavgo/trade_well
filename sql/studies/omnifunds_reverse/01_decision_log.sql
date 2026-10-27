-- The fund's decisions, rebuilt from the IBKR fills.
-- attach 'data/ibkr_2025oct_2026_oct.db' as i;
DROP TABLE IF EXISTS tx;   CREATE TABLE tx AS SELECT * FROM i.ibkr_transactions WHERE type IN ('Buy', 'Sell');
DROP TABLE IF EXISTS pos;  CREATE TABLE pos AS SELECT date, symbol, shares, mark, shares * mark AS val FROM i.ibkr_position_value WHERE shares > 0;
DROP TABLE IF EXISTS eq;   CREATE TABLE eq AS SELECT * FROM i.ibkr_equity_daily;

-- names held and the largest weight, per day
DROP TABLE IF EXISTS daily;
CREATE TABLE daily AS
SELECT p.date, COUNT(*) AS n, MAX(p.val) / e.equity AS maxw, SUM(p.val) / e.equity AS invested, e.equity
FROM pos p JOIN eq e USING (date) GROUP BY p.date;

-- held after each day's fills, and the previous session's holdings
DROP TABLE IF EXISTS held;     CREATE TABLE held AS SELECT date, symbol FROM pos;
DROP TABLE IF EXISTS heldprev;
CREATE TABLE heldprev AS
SELECT p2.date AS date, p.symbol
FROM pos p
JOIN (SELECT date, LEAD(date) OVER (ORDER BY date) AS nd FROM (SELECT DISTINCT date FROM pos)) x ON x.date = p.date
JOIN (SELECT DISTINCT date FROM pos) p2 ON p2.date = x.nd;

DROP TABLE IF EXISTS newbuy;
CREATE TABLE newbuy AS SELECT h.date, h.symbol FROM held h
WHERE NOT EXISTS (SELECT 1 FROM heldprev hp WHERE hp.date = h.date AND hp.symbol = h.symbol);
DROP TABLE IF EXISTS sold;
CREATE TABLE sold AS SELECT hp.date, hp.symbol FROM heldprev hp
WHERE NOT EXISTS (SELECT 1 FROM held h WHERE h.date = hp.date AND h.symbol = hp.symbol);

-- consecutive held sessions per symbol (gaps and islands over the trading calendar)
DROP TABLE IF EXISTS stretch;
CREATE TABLE stretch AS
WITH d AS (SELECT DISTINCT date FROM pos),
     dn AS (SELECT date, ROW_NUMBER() OVER (ORDER BY date) AS rn FROM d),
     p AS (SELECT pos.symbol, dn.rn, dn.date FROM pos JOIN dn USING (date)),
     g AS (SELECT symbol, date, rn, rn - ROW_NUMBER() OVER (PARTITION BY symbol ORDER BY rn) AS grp FROM p)
SELECT symbol, grp, MIN(date) AS s, MAX(date) AS e, COUNT(*) AS days FROM g GROUP BY symbol, grp;
