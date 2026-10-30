-- Slice table: the best __TOP__ candidates of all sources by the lower of the two win rates, then by in-sample trades. The overlap join is quadratic, so only these are compared.
DROP TABLE IF EXISTS pick_top;

CREATE TABLE pick_top AS
SELECT * FROM pick_candidate
WHERE strategy_id NOT IN (SELECT strategy_id FROM pick_exclude)
ORDER BY score DESC, is_trades DESC, strategy_id
LIMIT __TOP__
