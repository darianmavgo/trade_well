-- Stage 4: the trades of the picked run for each strategy and pass.
DROP TABLE IF EXISTS temp.bs_pick_trades;
CREATE TABLE bs_pick_trades AS
SELECT t.strategy_id, t.pass, t.symbol, t.exit_date, t.win, t.return_pct
FROM temp.bs_trades t
JOIN temp.bs_pick p ON p.strategy_id = t.strategy_id AND p.pass = t.pass AND p.run = t.run;
