-- Close-to-close return of every active ETF in the universe on each daily session. The market
-- database holds unadjusted splits, so a move past __SPLIT_RATIO__x (or under its inverse) in one
-- session is a split and not price action. Those sessions are dropped here and counted in etf_dropped.
DROP TABLE IF EXISTS etf_close;

CREATE TABLE etf_close AS
SELECT
    b.symbol AS symbol,
    substr(b.Date, 1, 10) AS date,
    b.close / LAG(b.close) OVER (PARTITION BY b.symbol ORDER BY b.Date) AS ratio
FROM market.backtest_start b
JOIN universe.universe u ON u.symbol = b.symbol
WHERE u.is_etf = 1 AND u.active = 1 AND b.timeframe = '1d' AND length(b.Date) = 10 AND b.close > 0;

DROP TABLE IF EXISTS etf_ret;

CREATE TABLE etf_ret AS
SELECT symbol, date, ratio - 1 AS ret
FROM etf_close
WHERE ratio IS NOT NULL AND ratio <= __SPLIT_RATIO__ AND ratio >= 1.0 / __SPLIT_RATIO__;

DROP TABLE IF EXISTS etf_dropped;

CREATE TABLE etf_dropped AS
SELECT symbol, date, ratio
FROM etf_close
WHERE ratio IS NOT NULL AND (ratio > __SPLIT_RATIO__ OR ratio < 1.0 / __SPLIT_RATIO__);
