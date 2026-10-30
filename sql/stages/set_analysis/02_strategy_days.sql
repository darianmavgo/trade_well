-- Slice table: how often each strategy signals. entries_per_year divides by the strategy's own span, at least one year.
DROP TABLE IF EXISTS set_strategy_days;

CREATE TABLE set_strategy_days AS
SELECT
    m.strategy_id,
    m.role,
    COUNT(d.entry_date) AS entry_days,
    MIN(d.entry_date) AS first_day,
    MAX(d.entry_date) AS last_day,
    COUNT(d.entry_date) / MAX(1.0, (julianday(MAX(d.entry_date)) - julianday(MIN(d.entry_date))) / 365.25) AS entries_per_year
FROM set_member m
LEFT JOIN set_entry_day d ON d.strategy_id = m.strategy_id
GROUP BY m.strategy_id, m.role
