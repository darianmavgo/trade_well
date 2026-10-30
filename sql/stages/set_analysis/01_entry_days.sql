-- Slice table: the distinct days each strategy of the set signals an entry. set_signal, set_member and set_trade are loaded by runner.AnalyzeSet.
DROP TABLE IF EXISTS set_entry_day;

CREATE TABLE set_entry_day AS
SELECT DISTINCT strategy_id, entry_date
FROM set_signal;

CREATE INDEX idx_set_entry_day ON set_entry_day(strategy_id, entry_date)
