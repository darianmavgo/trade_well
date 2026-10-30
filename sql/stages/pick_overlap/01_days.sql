-- Slice table: entry days per top candidate, and an index on the days table for the pair join.
DROP TABLE IF EXISTS pick_days;

CREATE TABLE pick_days AS
SELECT strategy_id, COUNT(*) AS entry_days
FROM pick_entry_day
GROUP BY strategy_id;

CREATE INDEX IF NOT EXISTS idx_pick_entry_day ON pick_entry_day(entry_date, strategy_id)
