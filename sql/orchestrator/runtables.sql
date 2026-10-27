-- Statements of pkg/orchestrator/runtables, read with orchestratorsql.Q("runtables/<name>").
-- Each starts with a "-- name:" line.

-- name: prune
SELECT name FROM sqlite_master WHERE type = 'table' AND name LIKE '%\_\_r%' ESCAPE '\'
