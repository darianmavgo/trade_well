UPDATE manage_exit SET status = ?, exit_date = CASE WHEN exit_date_source = ? THEN exit_date ELSE ? END WHERE id = ? AND status = ?
