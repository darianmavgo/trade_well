SELECT COUNT(*) FROM stage_order
WHERE account_number = ? AND instruction = 'BUY'
  AND status IN ('SUBMITTING', 'SUBMITTED', 'SUBMITTED_PENDING_FILL')
  AND COALESCE(submitted_at, staged_at) >= ?
