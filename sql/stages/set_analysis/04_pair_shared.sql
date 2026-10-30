-- Slice table: days on which both strategies of a pair signal. A pair with no shared day has no row here.
DROP TABLE IF EXISTS set_pair_shared;

CREATE TABLE set_pair_shared AS
SELECT p.a_id, p.b_id, COUNT(*) AS shared_days
FROM set_pair p
CROSS JOIN set_entry_day x ON x.strategy_id = p.a_id
JOIN set_entry_day y ON y.strategy_id = p.b_id AND y.entry_date = x.entry_date
GROUP BY p.a_id, p.b_id
