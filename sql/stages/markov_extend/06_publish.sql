-- Append the new predictions and move the model's meta forward. trained_through is kept (it is
-- the last session the model learned from). A model saved without it gets its old last_date.
INSERT INTO model.markov_prediction (symbol, date, state, prob_bull, prob_bear, signal)
SELECT symbol, date, state, prob_bull, prob_bear, signal FROM markov_x_new;
INSERT INTO markov_x_meta (symbol, added, last_date)
SELECT symbol, COUNT(*), MAX(date) FROM markov_x_new GROUP BY symbol;
UPDATE model.markov_model_meta
SET trained_through = COALESCE(trained_through, last_date),
    bars = bars + (SELECT added FROM markov_x_meta x WHERE x.symbol = markov_model_meta.symbol),
    last_date = (SELECT last_date FROM markov_x_meta x WHERE x.symbol = markov_model_meta.symbol)
WHERE symbol IN (SELECT symbol FROM markov_x_meta)
