INSERT OR REPLACE INTO markov_prediction_live (symbol, date, state, prob_bull, prob_bear, signal, trained_through, scan_run_id, saved_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
