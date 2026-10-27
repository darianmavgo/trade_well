INSERT INTO live_vs_backtest_deployed (strategy_id, pass, run, source_path, source_size, source_mtime,
			archive_path, trades, win_rate, window_start, window_end, note, synced_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(strategy_id, pass) DO UPDATE SET run = excluded.run, source_path = excluded.source_path,
			source_size = excluded.source_size, source_mtime = excluded.source_mtime, archive_path = excluded.archive_path,
			trades = excluded.trades, win_rate = excluded.win_rate, window_start = excluded.window_start,
			window_end = excluded.window_end, note = excluded.note, synced_at = excluded.synced_at
