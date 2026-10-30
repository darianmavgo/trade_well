-- Slice table: the pairs worth comparing. kind member: every pair of the primary and the chosen members. kind candidate: the primary against each other candidate.
DROP TABLE IF EXISTS set_pair;

CREATE TABLE set_pair AS
SELECT a.strategy_id AS a_id, b.strategy_id AS b_id, 'member' AS kind
FROM set_member a
JOIN set_member b ON a.strategy_id < b.strategy_id
WHERE a.role <> 'candidate' AND b.role <> 'candidate';

INSERT INTO set_pair
SELECT p.strategy_id, c.strategy_id, 'candidate'
FROM set_member p
JOIN set_member c ON c.role = 'candidate'
WHERE p.role = 'primary'
