-- Stage 3: whole-window backtest rates read from a refdata strategies.db (a backtest ASSET), for
-- strategies not yet copied by a refresh. Go fills this table from the asset; the stage only
-- creates it, so it exists (empty) when there is no refdata database to read.
DROP TABLE IF EXISTS winrate_refs;
CREATE TABLE winrate_refs (strategy_id TEXT, all_trades INTEGER, all_win_rate REAL, all_evaluated_at TEXT);
