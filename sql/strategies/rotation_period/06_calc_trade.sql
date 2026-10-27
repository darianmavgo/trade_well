-- What is traded each period and on which session. Long and short trade the pick
-- itself on its first session of the period. Inverse trades the matched inverse ETF
-- on that same date, and a pick with no pair, or no bar for it that day, is skipped.
INSERT INTO rp_trade (period, trade_symbol, entry_date)
SELECT p.period, p.symbol, w.first_date
FROM rp_pick p
JOIN rp_period w ON w.symbol = p.symbol AND w.period = p.period
WHERE '__SIDE__' <> 'inverse'
UNION ALL
SELECT p.period, i.inverse, w.first_date
FROM rp_pick p
JOIN rp_period w ON w.symbol = p.symbol AND w.period = p.period
JOIN rp_inverse i ON i.symbol = p.symbol
JOIN rp_bar b ON b.symbol = i.inverse AND b.date = w.first_date
WHERE '__SIDE__' = 'inverse';
