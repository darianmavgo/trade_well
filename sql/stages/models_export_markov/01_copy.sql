-- A Markov model database holding only the signal symbols of the strategy rows in a bundle.
-- The destination already has the markov_model_schema tables. src is the attached full
-- model database and ref is the attached bundled reference database.
INSERT INTO markov_model_meta SELECT * FROM src.markov_model_meta WHERE symbol IN (SELECT UPPER(TRIM(signal_symbol)) FROM ref.markov_strategy);
INSERT INTO markov_prediction SELECT * FROM src.markov_prediction WHERE symbol IN (SELECT symbol FROM markov_model_meta)
