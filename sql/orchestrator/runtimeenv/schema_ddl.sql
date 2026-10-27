CREATE TABLE IF NOT EXISTS runtimeenv (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  environment TEXT,
  service TEXT,
  version TEXT,
  project TEXT,
  detected_at TIMESTAMP
);
