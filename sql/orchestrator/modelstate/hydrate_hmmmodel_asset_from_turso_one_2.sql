INSERT OR REPLACE INTO hmm_prediction
			(symbol, date, ret, vol, regime, regime_prob, next_regime, next_regime_prob, loglik_bar, in_sample)
			VALUES (?,?,?,?,?,?,?,?,?,?)
