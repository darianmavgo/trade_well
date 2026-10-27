SELECT date, state, prob_bull, prob_bear, signal FROM markov_prediction
		WHERE symbol = ? AND date > ? ORDER BY date
