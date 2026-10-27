-- Candidate list for the rotation rows. A symbol is a candidate if it was ever among the 1000
-- most liquid names at a close (table lq). The ranking inside the strategy is still point in
-- time: this list only decides which symbols the runner loads bars for, so it cannot pick a
-- winner with hindsight. A name that is only liquid today is not added, a name that stopped
-- trading is.
DROP TABLE IF EXISTS rotation_candidates;
CREATE TABLE rotation_candidates AS
SELECT symbol, MIN(date) AS first_liquid, MAX(date) AS last_liquid, COUNT(*) AS liquid_sessions
FROM lq GROUP BY symbol;
