-- Replace the batch's symbols in the persisted model (attached as model).
-- trained_through is the last session the model learned from: the cutoff, or the
-- last session when there is none (__THROUGH__ is '' then).
DELETE FROM model.markov_prediction WHERE symbol IN (__SYMBOL_LIST__);
INSERT INTO model.markov_prediction (symbol, date, state, prob_bull, prob_bear, signal)
SELECT symbol, date, state, prob_bull, prob_bear, signal FROM markov_batch_prediction;
DELETE FROM model.markov_model_meta WHERE symbol IN (__SYMBOL_LIST__);
INSERT INTO model.markov_model_meta (symbol, bars, first_date, last_date, trained_at, lookback_days, state_threshold, trained_through)
SELECT symbol, COUNT(*), MIN(date), MAX(date), datetime('now'), 20, 0.05,
       COALESCE(NULLIF('__THROUGH__', ''), MAX(date))
FROM markov_batch_prediction GROUP BY symbol;
