-- One row about the primary and chosen members together. collision_pct is the share of signal days on which two or more strategies signal.
DROP TABLE IF EXISTS set_summary;

CREATE TABLE set_summary AS
SELECT
    COUNT(*) AS signal_days,
    SUM(strategies) AS signals,
    SUM(CASE WHEN strategies >= 2 THEN 1 ELSE 0 END) AS collision_days,
    SUM(CASE WHEN strategies >= 2 THEN 1 ELSE 0 END) * 1.0 / MAX(1, COUNT(*)) AS collision_pct,
    MAX(strategies) AS max_per_day,
    MIN(entry_date) AS first_day,
    MAX(entry_date) AS last_day
FROM set_day_load;

DROP TABLE IF EXISTS set_pair_summary;

CREATE TABLE set_pair_summary AS
SELECT
    COUNT(*) AS pairs,
    AVG(overlap_pct) AS avg_overlap_pct,
    MAX(overlap_pct) AS max_overlap_pct,
    AVG(jaccard) AS avg_jaccard
FROM set_pair_overlap
WHERE kind = 'member'
