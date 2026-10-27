-- State of one pipeline run, kept in pipeline.db in the run folder.
-- pipeline_run is the run itself, one row.
-- pipeline_scope is what the run may touch, kind symbol or strategy.
-- pipeline_step is each step in order with its status, so an interrupted run resumes.
CREATE TABLE IF NOT EXISTS pipeline_run (
    run_id INTEGER PRIMARY KEY,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    park TEXT NOT NULL,
    bench TEXT NOT NULL,
    primary_id TEXT NOT NULL,
    explicit_strategies INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS pipeline_scope (
    kind TEXT NOT NULL,
    id TEXT NOT NULL,
    PRIMARY KEY (kind, id)
);

CREATE TABLE IF NOT EXISTS pipeline_step (
    seq INTEGER NOT NULL,
    name TEXT PRIMARY KEY,
    status TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    error TEXT
);
