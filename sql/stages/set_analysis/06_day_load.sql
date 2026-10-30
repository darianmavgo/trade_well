-- Slice table: how many of the primary and chosen members signal on each day.
DROP TABLE IF EXISTS set_day_load;

CREATE TABLE set_day_load AS
SELECT d.entry_date, COUNT(*) AS strategies
FROM set_entry_day d
JOIN set_member m ON m.strategy_id = d.strategy_id
WHERE m.role <> 'candidate'
GROUP BY d.entry_date
