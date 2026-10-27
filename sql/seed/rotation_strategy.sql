-- Rotation rows for refdata/strategies.db. Re-runnable: it replaces these ids only.
--
--   sqlite3 refdata/strategies.db \
--     -cmd "attach 'rotation_candidates.db' as cand" < sql/seed/rotation_strategy.sql
--
-- cand.rotation_candidates comes from sql/studies/omnifunds_reverse/06_candidates.sql
-- (every symbol that was ever one of the 1,000 most liquid at a close). Each row
-- carries that list in `symbols`, because the runner loads bars only for the
-- symbols a strategy names. The ranking inside the strategy is point in time.
--
-- Columns: universe_size = names ranked each session (most liquid first),
-- top_k = names held, exit_buffer = a held name is sold once ranked worse than
-- top_k + exit_buffer, max_weight_pct = cap on one name, regime_sma = 0 for no
-- market gate or the QQQ session average that must be beaten to hold anything.
CREATE TABLE IF NOT EXISTS rotation_strategy (
	id TEXT PRIMARY KEY, name TEXT NOT NULL, symbols TEXT NOT NULL DEFAULT '',
	universe_size INTEGER NOT NULL, top_k INTEGER NOT NULL, exit_buffer INTEGER NOT NULL,
	max_weight_pct REAL NOT NULL, regime_symbol TEXT NOT NULL DEFAULT 'QQQ',
	regime_sma INTEGER NOT NULL DEFAULT 0, allocation_pct REAL NOT NULL,
	cash_yield REAL NOT NULL, slippage_pct REAL NOT NULL
);

WITH spec(id, name, top_k, exit_buffer, max_weight_pct, regime_sma) AS (VALUES
    ('rotation-top3-liquid1000',        'Rotation top 3 of 1000 liquid',               3, 2, 0.50,   0),
    ('rotation-top5-liquid1000',        'Rotation top 5 of 1000 liquid',               5, 3, 0.50,   0),
    ('rotation-top10-liquid1000',       'Rotation top 10 of 1000 liquid',             10, 5, 0.25,   0),
    ('rotation-top5-liquid1000-qqq200', 'Rotation top 5 of 1000 liquid, QQQ>SMA200',   5, 3, 0.50, 200),
    ('rotation-top10-liquid1000-qqq200','Rotation top 10 of 1000 liquid, QQQ>SMA200', 10, 5, 0.25, 200)
)
INSERT OR REPLACE INTO rotation_strategy
    (id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma, allocation_pct, cash_yield, slippage_pct)
SELECT id, name, (SELECT group_concat(symbol, ',') FROM (SELECT symbol FROM cand.rotation_candidates ORDER BY symbol)),
       1000, top_k, exit_buffer, max_weight_pct, 'QQQ', regime_sma, 1.0, 0, 0.0005
FROM spec;
