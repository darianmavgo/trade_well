SELECT date, state, prob, next_prob FROM hmm_state_prob_live
		WHERE symbol = ? AND trained_at = ? AND date > ? ORDER BY date, state
