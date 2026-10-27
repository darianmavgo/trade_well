-- Statements of pkg/orchestrator/runtimeenv, read with orchestratorsql.Q("runtimeenv/<name>").
-- Each starts with a "-- name:" line.

-- name: schema_ddl
CREATE TABLE IF NOT EXISTS runtimeenv (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  environment TEXT,
  service TEXT,
  version TEXT,
  project TEXT,
  detected_at TIMESTAMP
);
