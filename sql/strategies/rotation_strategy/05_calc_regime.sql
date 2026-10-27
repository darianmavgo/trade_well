-- Market gate. risk_on is 1 when __REGIME_SYMBOL__ closes above its
-- __REGIME_SMA__ session average (averaged over sessions from 400 calendar days
-- before the window). With __REGIME_ON__ = 0 every session the ranking covers is
-- risk on and the regime symbol is not read.
INSERT INTO rot_regime (date, risk_on)
SELECT DISTINCT date, 1 FROM rot_rank WHERE __REGIME_ON__ = 0;

INSERT INTO rot_regime (date, risk_on)
WITH bars AS (
    SELECT substr(Date, 1, 10) AS date, close,
           AVG(close) OVER (ORDER BY Date ROWS BETWEEN __REGIME_PRECEDING__ PRECEDING AND CURRENT ROW) AS sma,
           COUNT(*) OVER (ORDER BY Date ROWS BETWEEN __REGIME_PRECEDING__ PRECEDING AND CURRENT ROW) AS n
    FROM backtest_start
    WHERE __REGIME_ON__ = 1 AND symbol = '__REGIME_SYMBOL__' AND length(Date) = 10
      AND substr(Date, 1, 10) >= date('__START_DATE__', '-400 day')
      AND substr(Date, 1, 10) <= '__END_DATE__'
)
SELECT date, CASE WHEN n >= __REGIME_SMA__ AND close > sma THEN 1 ELSE 0 END FROM bars;
