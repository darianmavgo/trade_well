-- The model is trained by train markov and persisted in the markov models
-- database. Backtests only read this symbol's rows. Nothing is recomputed here.
ATTACH DATABASE '__MARKOV_DB__' AS markov;

CREATE TABLE IF NOT EXISTS markov_model_predictions AS
SELECT
    symbol,
    date AS Date,
    state AS current_state,
    prob_bull,
    prob_bear,
    signal
FROM markov.markov_prediction
WHERE symbol = '__SYMBOL__';
