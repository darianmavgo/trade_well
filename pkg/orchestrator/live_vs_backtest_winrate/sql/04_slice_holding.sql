-- Stage 4b: positions still open or waiting to fill, per strategy.
DROP TABLE IF EXISTS winrate_holding;
CREATE TABLE winrate_holding AS
SELECT COALESCE(NULLIF(strategy_id, ''), strategy_name) AS strategy_id,
	SUM(CASE WHEN status = 'OPEN' THEN 1 ELSE 0 END) AS open_n,
	SUM(CASE WHEN status = 'PENDING_FILL' THEN 1 ELSE 0 END) AS pending_n
FROM manage_exit
GROUP BY 1;
