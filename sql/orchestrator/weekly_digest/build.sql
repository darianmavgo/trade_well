SELECT status, COUNT(*) FROM stage_order WHERE staged_at >= ? GROUP BY status ORDER BY status
