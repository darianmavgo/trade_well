-- Stage 2b: per-symbol tradability screen over the last ~2 years of bars.
-- Needs: search_02_daily_ret and `.parameter set :min_adv <dollars>` (README.md).
-- Keeps symbols a real account could trade.
DROP TABLE IF EXISTS search_03_liquidity;
CREATE TABLE search_03_liquidity AS
SELECT symbol,
       AVG(close * volume)  AS avg_dollar_vol,
       MIN(close)           AS min_close,
       MAX(ABS(ret))        AS max_abs_ret,
       COUNT(*)             AS n_days
FROM search_02_daily_ret
WHERE day >= (SELECT date(MAX(day), '-2 years') FROM search_02_daily_ret)
GROUP BY symbol
HAVING AVG(close * volume) >= :min_adv
   AND MIN(close) >= 5
   AND MAX(ABS(ret)) <= 0.5
   AND COUNT(*) >= 400;
