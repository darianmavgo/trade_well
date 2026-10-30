-- Slice table: days both strategies of an ordered pair entered. Both orders are stored. A pair that never shares a day has no row.
DROP TABLE IF EXISTS pick_pair_shared;

CREATE TABLE pick_pair_shared AS
SELECT x.strategy_id AS a_id, y.strategy_id AS b_id, COUNT(*) AS shared_days
FROM pick_entry_day x
JOIN pick_entry_day y ON y.entry_date = x.entry_date AND y.strategy_id <> x.strategy_id
GROUP BY x.strategy_id, y.strategy_id
