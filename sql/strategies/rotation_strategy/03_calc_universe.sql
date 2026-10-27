-- The tradable universe each session: the __UNIVERSE_SIZE__ most liquid names by
-- 60 session average dollar volume as of that close (point in time, so a name
-- that became liquid later, or stopped trading, is not looked at with hindsight).
-- A name needs 127 sessions of history for the 126 session lookback, and a
-- session whose one day move is over 50% is dropped as a split or bad print.
INSERT INTO rot_universe (symbol, date, liq_rank)
SELECT symbol, date, liq_rank FROM (
    SELECT symbol, date,
           ROW_NUMBER() OVER (PARTITION BY date ORDER BY dv60 DESC, symbol) AS liq_rank
    FROM rot_feature
    WHERE hist >= 127 AND r126 IS NOT NULL AND abs(r1) < 0.5
      AND date >= '__START_DATE__'
)
WHERE liq_rank <= __UNIVERSE_SIZE__;

CREATE INDEX rot_universe_sd ON rot_universe (symbol, date);
CREATE INDEX rot_universe_d ON rot_universe (date);
