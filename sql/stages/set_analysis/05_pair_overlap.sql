-- Slice table: entry-day overlap per pair. overlap_pct is shared days over the smaller strategy's days (1 means the smaller one only signals when the other does). jaccard is shared over all days either signals.
DROP TABLE IF EXISTS set_pair_overlap;

CREATE TABLE set_pair_overlap AS
SELECT
    p.a_id,
    p.b_id,
    p.kind,
    da.entry_days AS a_days,
    db.entry_days AS b_days,
    COALESCE(s.shared_days, 0) AS shared_days,
    COALESCE(s.shared_days, 0) * 1.0 / MAX(1, MIN(da.entry_days, db.entry_days)) AS overlap_pct,
    COALESCE(s.shared_days, 0) * 1.0 / MAX(1, da.entry_days + db.entry_days - COALESCE(s.shared_days, 0)) AS jaccard
FROM set_pair p
JOIN set_strategy_days da ON da.strategy_id = p.a_id
JOIN set_strategy_days db ON db.strategy_id = p.b_id
LEFT JOIN set_pair_shared s ON s.a_id = p.a_id AND s.b_id = p.b_id
