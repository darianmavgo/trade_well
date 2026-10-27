SELECT date, adj_close FROM daily_bars_live WHERE symbol = ? AND date >= ? ORDER BY date
