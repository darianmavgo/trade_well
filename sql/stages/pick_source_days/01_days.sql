-- Appends the days each top candidate of source __SRC__ entered a trade, in-sample and held-out together.
INSERT INTO pick_entry_day
SELECT DISTINCT t.strategy_id, substr(t.entry_date, 1, 10)
FROM __INS__.trades t
JOIN pick_top p ON p.strategy_id = t.strategy_id AND p.src = __SRC__;

INSERT INTO pick_entry_day
SELECT DISTINCT t.strategy_id, substr(t.entry_date, 1, 10)
FROM __OOS__.trades t
JOIN pick_top p ON p.strategy_id = t.strategy_id AND p.src = __SRC__
