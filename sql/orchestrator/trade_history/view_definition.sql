-- The definition SQLite has stored for a view, to compare with the one in this build.
SELECT sql FROM sqlite_master WHERE type = 'view' AND name = ?;
