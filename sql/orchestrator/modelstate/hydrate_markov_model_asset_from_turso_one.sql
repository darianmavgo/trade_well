SELECT date, state, prob_bull, prob_bear, signal FROM markov_extension_live
		WHERE symbol = ? AND trained_at = ? AND date > ? ORDER BY date
