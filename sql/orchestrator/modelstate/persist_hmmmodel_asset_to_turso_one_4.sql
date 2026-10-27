SELECT date, state, prob, next_prob FROM hmm_state_prob
		WHERE symbol = ? AND date > ? ORDER BY date, state
