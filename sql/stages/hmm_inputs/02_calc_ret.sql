-- Daily log return of the adjusted close. The first bar has no prior close, so no return.
-- __SYMBOL_LIST__ is the quoted symbols.
INSERT INTO hmm_ret (symbol, date, ret)
SELECT
    symbol,
    date,
    ln(adj_close / LAG(adj_close) OVER w)
FROM bars.daily_bar
WHERE symbol IN (__SYMBOL_LIST__)
WINDOW w AS (PARTITION BY symbol ORDER BY date);
