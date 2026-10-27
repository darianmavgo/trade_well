SELECT status, COUNT(*) FROM submit_order WHERE submitted_at >= ? GROUP BY status ORDER BY status
