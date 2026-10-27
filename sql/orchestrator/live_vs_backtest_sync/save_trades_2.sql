SELECT symbol, exit_date, win, return_pct FROM bs_pick_trades WHERE strategy_id = ? AND pass = ? ORDER BY exit_date
