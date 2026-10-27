UPDATE markov_model_meta SET trained_through = COALESCE(trained_through, last_date), bars = bars + ?, last_date = ? WHERE symbol = ?
