-- Each session, the 1000 most liquid names by 60 session average dollar volume known at that
-- close. No name is chosen with hindsight.
DROP TABLE IF EXISTS lq;
CREATE TABLE lq AS
SELECT symbol, date FROM (
    SELECT symbol, date, ROW_NUMBER() OVER (PARTITION BY date ORDER BY dv60 DESC) AS lr FROM f
) WHERE lr <= 1000;
CREATE INDEX lq_sd ON lq (symbol, date);
