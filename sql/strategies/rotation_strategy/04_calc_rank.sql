-- Score each universe name by the mean of five cross sectional percentile ranks
-- within that session's universe: 21, 63 and 126 session return, distance above
-- the 50 session average, and distance from the 63 session high. Rank 1 is the
-- strongest. Ties break on symbol so a rerun gives the same book.
INSERT INTO rot_rank (symbol, date, score, rank)
WITH scored AS (
    SELECT f.symbol, f.date,
           (PERCENT_RANK() OVER (PARTITION BY f.date ORDER BY f.r21)
          + PERCENT_RANK() OVER (PARTITION BY f.date ORDER BY f.r63)
          + PERCENT_RANK() OVER (PARTITION BY f.date ORDER BY f.r126)
          + PERCENT_RANK() OVER (PARTITION BY f.date ORDER BY f.ma50)
          + PERCENT_RANK() OVER (PARTITION BY f.date ORDER BY f.dd63)) / 5.0 AS score
    FROM rot_universe u
    JOIN rot_feature f ON f.symbol = u.symbol AND f.date = u.date
)
SELECT symbol, date, score,
       ROW_NUMBER() OVER (PARTITION BY date ORDER BY score DESC, symbol)
FROM scored;

CREATE INDEX rot_rank_sd ON rot_rank (symbol, date);
