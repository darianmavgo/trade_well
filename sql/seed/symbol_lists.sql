-- Symbol lists for refdata/strategies.db. Re-runnable: it replaces these ids only.
--
--   sqlite3 refdata/strategies.db -cmd "attach 'refdata/universe.db' as u" < sql/seed/symbol_lists.sql
--
-- etf-pre-2021: every ETF in the universe whose first trade date is before
-- 2021-01-01 (is_etf = 1, first_trade_date known), sorted, comma separated.
INSERT OR REPLACE INTO symbol_lists (symbol_list_id, list)
SELECT 'etf-pre-2021', group_concat(symbol, ',')
FROM (
    SELECT symbol FROM u.universe
    WHERE is_etf = 1 AND first_trade_date <> '' AND first_trade_date < '2021-01-01'
    ORDER BY symbol
);

-- etf-pre-2021-unleveraged: etf-pre-2021 without the leveraged funds (leverage
-- 1.5x, 2x or 3x, long or inverse). Unleveraged inverse funds (SH, VXX, ...) stay.
INSERT OR REPLACE INTO symbol_lists (symbol_list_id, list)
SELECT 'etf-pre-2021-unleveraged', group_concat(symbol, ',')
FROM (
    SELECT symbol FROM u.universe
    WHERE is_etf = 1 AND first_trade_date <> '' AND first_trade_date < '2021-01-01' AND leverage = 'none'
    ORDER BY symbol
);

-- etf-2x-pre-2021: only the 2x leveraged ETFs (long and inverse) whose first
-- trade date is before 2021-01-01, so they have history to backtest.
INSERT OR REPLACE INTO symbol_lists (symbol_list_id, list)
SELECT 'etf-2x-pre-2021', group_concat(symbol, ',')
FROM (
    SELECT symbol FROM u.universe
    WHERE is_etf = 1 AND first_trade_date <> '' AND first_trade_date < '2021-01-01' AND leverage = '2x'
    ORDER BY symbol
);

-- etf-3x-pre-2021: only the 3x leveraged ETFs (long and inverse) whose first
-- trade date is before 2021-01-01, so they have history to backtest.
INSERT OR REPLACE INTO symbol_lists (symbol_list_id, list)
SELECT 'etf-3x-pre-2021', group_concat(symbol, ',')
FROM (
    SELECT symbol FROM u.universe
    WHERE is_etf = 1 AND first_trade_date <> '' AND first_trade_date < '2021-01-01' AND leverage = '3x'
    ORDER BY symbol
);

-- etf-2x-sector-pairs: 25 sectors of the market, each as a 2x leveraged long ETF and
-- its matched 2x inverse (50 symbols), all with daily bars in the market database.
-- Long / inverse, by sector:
--   SSO  SDS   S&P 500
--   DDM  DXD   Dow 30
--   QLD  QID   Nasdaq-100
--   UWM  TWM   Russell 2000
--   ROM  REW   technology
--   USD  SSG   semiconductors
--   UYG  SKF   financials
--   ERX  ERY   energy
--   RXL  RXD   health care
--   BIB  BIS   biotech
--   UCC  SCC   consumer services
--   UGE  SZK   consumer goods
--   UXI  SIJ   industrials
--   UYM  SMN   materials
--   URE  SRS   real estate
--   UPW  SDP   utilities
--   UGL  GLL   gold
--   NUGT DUST  gold miners
--   AGQ  ZSL   silver
--   UCO  SCO   crude oil
--   BOIL KOLD  natural gas
--   UBT  TBT   20+ year Treasuries
--   EET  EEV   emerging markets
--   EZJ  EWV   Japan
--   XPP  FXP   China
INSERT OR REPLACE INTO symbol_lists (symbol_list_id, list)
VALUES ('etf-2x-sector-pairs', 'AGQ,BIB,BIS,BOIL,DDM,DUST,DXD,EET,EEV,ERX,ERY,EWV,EZJ,FXP,GLL,KOLD,NUGT,QID,QLD,REW,ROM,RXD,RXL,SCC,SCO,SDP,SDS,SIJ,SKF,SMN,SRS,SSG,SSO,SZK,TBT,TWM,UBT,UCC,UCO,UGE,UGL,UPW,URE,USD,UWM,UXI,UYG,UYM,XPP,ZSL');


-- spx-3x: the two 3x S&P 500 bull funds. SPXL is Direxion Daily S&P 500 Bull 3X
-- Shares; UPRO is ProShares UltraPro S&P500. Both aim for 3x the daily S&P 500 return.
INSERT OR REPLACE INTO symbol_lists (symbol_list_id, list)
VALUES ('spx-3x', 'SPXL,UPRO');
