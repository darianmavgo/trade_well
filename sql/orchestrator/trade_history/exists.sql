-- Are trade_history and the view Capture reads there? A dry run asks before it counts anything.
SELECT COUNT(*) FROM sqlite_master WHERE name IN ('trade_history', 'trade_history_candidate');
