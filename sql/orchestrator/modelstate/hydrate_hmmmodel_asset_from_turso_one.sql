SELECT date, ret, vol, regime, regime_prob, next_regime, next_regime_prob, loglik_bar, in_sample
		FROM hmm_prediction_live WHERE symbol = ? AND trained_at = ? AND date > ? ORDER BY date
