-- First 100 ids from each strategies.db strategy table (400 total).
SELECT id FROM (SELECT id FROM streak_strategy     ORDER BY id LIMIT 100)
UNION ALL
SELECT id FROM (SELECT id FROM hold_strategy       ORDER BY id LIMIT 100)
UNION ALL
SELECT id FROM (SELECT id FROM tree_strategy       ORDER BY id LIMIT 100)
UNION ALL
SELECT id FROM (SELECT id FROM markov_strategy     ORDER BY id LIMIT 100);
