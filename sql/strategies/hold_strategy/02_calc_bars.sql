-- The window's bars priced on the run's basis, numbered, with the re-entry
-- average over them. The average covers the bars of the window only, so it is
-- empty for the first period bars. With __SMA_PERIOD__ = 0 the average is the bar's
-- own close and the entry rule below never reads it.
INSERT INTO hold_strategy_bars (idx, symbol, date, open, high, low, close, volume, rn, sma)
WITH priced AS (
    SELECT
        coalesce(idx, rowid, 0) AS idx, symbol, substr(Date, 1, 10) AS date, volume,
        CASE WHEN __TOTAL_RETURN__ = 1 AND "Adj Close" > 0 AND close > 0 THEN "Adj Close" / close ELSE 1.0 END AS f,
        open, high, low, close, Date AS raw_date
    FROM backtest_start
    WHERE symbol = '__TRADE_SYMBOL__' AND length(Date) = 10
      AND substr(Date, 1, 10) >= '__START_DATE__' AND substr(Date, 1, 10) <= '__END_DATE__'
)
SELECT
    idx, symbol, date,
    open * f, high * f, low * f, close * f, volume,
    ROW_NUMBER() OVER w,
    AVG(close * f) OVER (ORDER BY raw_date ROWS BETWEEN __SMA_PRECEDING__ PRECEDING AND CURRENT ROW)
FROM priced
WINDOW w AS (ORDER BY raw_date);
