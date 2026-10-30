-- Adds the best remaining candidate whose largest entry-day overlap with the strategies already chosen is at most __MAX_OVERLAP__. With none chosen yet it adds the best candidate. Adds nothing when none qualifies. Run once per round by runner.PickSet.
INSERT INTO pick (round, strategy_id, max_overlap)
SELECT __ROUND__, c.strategy_id, COALESCE(MAX(o.overlap_pct), 0) AS worst
FROM pick_top c
LEFT JOIN pick p ON p.strategy_id <> c.strategy_id
LEFT JOIN pick_overlap o ON o.a_id = c.strategy_id AND o.b_id = p.strategy_id
WHERE c.strategy_id NOT IN (SELECT strategy_id FROM pick)
GROUP BY c.strategy_id
HAVING worst <= __MAX_OVERLAP__
ORDER BY c.score DESC, c.is_trades DESC, c.strategy_id
LIMIT 1
