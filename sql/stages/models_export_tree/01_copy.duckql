-- A tree model database holding only the signal symbols of the strategy rows in a bundle.
-- The destination already has the tree_model_schema tables. src is the attached full
-- model database and ref is the attached bundled reference database.
INSERT INTO tree_model_meta (symbol, samples, grown_cases, class_counts, first_date, last_date, trained_at) SELECT symbol, samples, grown_cases, class_counts, first_date, last_date, trained_at FROM src.tree_model_meta WHERE symbol IN (SELECT UPPER(TRIM(signal_symbol)) FROM ref.tree_strategy);
INSERT INTO tree_node SELECT * FROM src.tree_node WHERE symbol IN (SELECT symbol FROM tree_model_meta)
