SELECT date, regime, regime_prob, next_regime, next_regime_prob
		FROM hmm_prediction_live WHERE symbol = ? AND trained_at = ? ORDER BY date DESC LIMIT 1
