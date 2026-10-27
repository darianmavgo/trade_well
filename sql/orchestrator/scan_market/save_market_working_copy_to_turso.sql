SELECT substr(CAST(Date AS TEXT), 1, 10) AS d, open, high, low, close, COALESCE("Adj Close", close) AS adj, COALESCE(volume, 0) AS volume
			 FROM backtest_start
			 WHERE symbol = ? AND timeframe = '1d' AND length(CAST(Date AS TEXT)) = 10 AND CAST(Date AS TEXT) >= ?
			 ORDER BY Date
