-- Stage 6: one row per strategy that is greenlit (winrate_ids) or has data on any side.
DROP TABLE IF EXISTS winrate_all_ids;
CREATE TABLE winrate_all_ids AS
SELECT strategy_id FROM winrate_ids
UNION SELECT strategy_id FROM winrate_backtest
UNION SELECT strategy_id FROM winrate_live WHERE strategy_id <> ''
UNION SELECT strategy_id FROM winrate_holding WHERE strategy_id <> '';

DROP TABLE IF EXISTS winrate_joined;
CREATE TABLE winrate_joined AS
SELECT i.strategy_id,
	(SELECT 1 FROM winrate_ids g WHERE g.strategy_id = i.strategy_id) IS NOT NULL AS greenlit,
	b.is_trades, b.is_win_rate, b.oos_trades, b.oos_win_rate,
	COALESCE(b.all_win_rate, r.all_win_rate) AS all_win_rate,
	COALESCE(b.all_trades, r.all_trades) AS all_trades,
	COALESCE(b.all_evaluated_at, r.all_evaluated_at) AS all_evaluated_at,
	b.evaluated_at, b.source,
	COALESCE(l.live_n, 0) AS live_n,
	COALESCE(l.live_fills, 0) AS live_fills,
	COALESCE(l.live_wins, 0) AS live_wins,
	CASE WHEN l.live_n > 0 THEN 1.0 * l.live_wins / l.live_n END AS live_win_rate,
	l.live_avg_return,
	COALESCE(h.open_n, 0) AS open_n,
	COALESCE(h.pending_n, 0) AS pending_n,
	COALESCE(m.model_n, 0) AS model_n,
	CASE WHEN m.model_n > 0 THEN 1.0 * m.model_wins / m.model_n END AS model_win_rate,
	l.first_close, l.last_close
FROM winrate_all_ids i
LEFT JOIN winrate_backtest b ON b.strategy_id = i.strategy_id
LEFT JOIN winrate_refs r ON r.strategy_id = i.strategy_id
LEFT JOIN winrate_live l ON l.strategy_id = i.strategy_id
LEFT JOIN winrate_holding h ON h.strategy_id = i.strategy_id
LEFT JOIN winrate_model m ON m.strategy_id = i.strategy_id;
