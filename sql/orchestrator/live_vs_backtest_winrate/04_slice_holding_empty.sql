-- Stage 4b when manage_exit does not exist yet.
DROP TABLE IF EXISTS winrate_holding;
CREATE TABLE winrate_holding (strategy_id TEXT, open_n INTEGER, pending_n INTEGER);
