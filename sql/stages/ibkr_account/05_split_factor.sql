-- The statement records shares as traded and the bars are split-adjusted. A fill whose price is a whole
-- multiple of the adjusted close (10 for a 10-for-1 split) is on the old basis, so its shares are multiplied
-- by that factor to land on the bars' basis. Ratios near 1 are ordinary intraday differences. A ratio
-- below one half is a reverse split and becomes 1 over the whole number.
CREATE TABLE ibkr_fill_factor AS
SELECT txn_id,
       CASE WHEN ratio >= 1.8 THEN ROUND(ratio)
            WHEN ratio <= 0.55 THEN 1.0 / ROUND(1.0 / ratio)
            ELSE 1.0 END AS factor
FROM ibkr_price_check
