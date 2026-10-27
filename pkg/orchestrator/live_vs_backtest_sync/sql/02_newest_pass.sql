-- Stage 2: the newest run of each pass on its own, the fallback when no run has both.
DROP TABLE IF EXISTS temp.bs_newest;
CREATE TABLE bs_newest AS
SELECT strategy_id, pass, MAX(run) AS run
FROM temp.bs_found
GROUP BY strategy_id, pass;
