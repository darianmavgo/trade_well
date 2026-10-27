-- A symbol's return for a period: last close against first open. rp_seq numbers
-- the periods that have any bar, so "previous period" is the one before it.
INSERT INTO rp_period (symbol, period, first_date, first_open, last_date, last_close, ret)
SELECT
    f.symbol, f.period, f.date, f.open, l.date, l.close,
    (l.close - f.open) / f.open
FROM rp_bar f
JOIN rp_bar l ON l.symbol = f.symbol AND l.period = f.period AND l.rn_last = 1
WHERE f.rn_first = 1 AND f.open <> 0;

INSERT INTO rp_seq (period, seq)
SELECT period, ROW_NUMBER() OVER (ORDER BY period) FROM (SELECT DISTINCT period FROM rp_bar);
