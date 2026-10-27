CREATE TABLE IF NOT EXISTS account_allowlist (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_number TEXT,
  enabled BOOLEAN,
  loaded_at TIMESTAMP
);
