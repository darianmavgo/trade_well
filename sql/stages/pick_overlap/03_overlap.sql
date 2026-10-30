-- Slice table: shared days over the smaller strategy's days, for each ordered pair that shares a day. Then the empty table of chosen strategies.
DROP TABLE IF EXISTS pick_overlap;

CREATE TABLE pick_overlap AS
SELECT s.a_id, s.b_id, s.shared_days * 1.0 / MIN(da.entry_days, db.entry_days) AS overlap_pct
FROM pick_pair_shared s
JOIN pick_days da ON da.strategy_id = s.a_id
JOIN pick_days db ON db.strategy_id = s.b_id;

CREATE INDEX idx_pick_overlap ON pick_overlap(a_id, b_id);

DROP TABLE IF EXISTS pick;

CREATE TABLE pick (round INTEGER, strategy_id TEXT PRIMARY KEY, max_overlap REAL)
