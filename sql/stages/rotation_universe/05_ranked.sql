-- The liquid universe ranked by a momentum composite. The composite is the mean of five percentile
-- ranks: 21, 63 and 126 session return, distance above the 50 session average and distance from
-- the 63 session high. A session move over 50% is a split or a bad print and is dropped, as in
-- sql/strategies/rotation_strategy. The first session is 2021-06-01 (126 sessions of lookback).
DROP TABLE IF EXISTS u;
CREATE TABLE u AS
SELECT f.* FROM f JOIN lq USING (symbol, date)
WHERE ABS(r1) < 0.5 AND r126 IS NOT NULL AND date >= '2021-06-01';
CREATE INDEX u_d ON u (date);

DROP TABLE IF EXISTS sc;
CREATE TABLE sc AS
SELECT symbol, date, f1, f5, r1,
       (PERCENT_RANK() OVER (PARTITION BY date ORDER BY r21) + PERCENT_RANK() OVER (PARTITION BY date ORDER BY r63)
      + PERCENT_RANK() OVER (PARTITION BY date ORDER BY r126) + PERCENT_RANK() OVER (PARTITION BY date ORDER BY ma50)
      + PERCENT_RANK() OVER (PARTITION BY date ORDER BY dd63)) / 5.0 AS s
FROM u;

DROP TABLE IF EXISTS rk;
CREATE TABLE rk AS
SELECT symbol, date, f1, f5, s, ROW_NUMBER() OVER (PARTITION BY date ORDER BY s DESC) AS rn
FROM sc WHERE f1 IS NOT NULL AND ABS(f1) < 0.5;
CREATE INDEX rk_d ON rk (date, rn);
