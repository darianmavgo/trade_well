-- Price action reclaim rows for refdata/strategies.db: one row per non-leveraged ETF. The symbols
-- are the symbol list etf-pre-2021-unleveraged (ETFs trading before 2021, leveraged funds left
-- out), so each has enough history for the 200-bar average. Re-runnable: it adds ids that are not
-- there and leaves every row it finds alone, so a row tuned by hand stays as it is.
--
--   sqlite3 refdata/strategies.db < sql/seed/price_action_reclaim_strategy.sql
--
-- A row takes the table's defaults: a 200-bar trend average, support from 25 to 5 bars back, a stop
-- 0.5% under support skipped when over 15% away, a +10% take-profit, a 20-session hold and 10% of
-- equity per position (the setup of the universe-wide strategy this family replaces).
CREATE TABLE IF NOT EXISTS price_action_reclaim_strategy (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	symbol           TEXT NOT NULL,
	sma_period       INTEGER NOT NULL DEFAULT 200,
	support_bars     INTEGER NOT NULL DEFAULT 25,
	reclaim_gap      INTEGER NOT NULL DEFAULT 5,
	stop_cushion_pct REAL NOT NULL DEFAULT 0.005,
	max_stop_pct     REAL NOT NULL DEFAULT 0.15,
	take_profit_pct  REAL NOT NULL DEFAULT 0.10,
	hold_days        INTEGER NOT NULL DEFAULT 20,
	allocation_pct   REAL NOT NULL DEFAULT 0.10,
	cash_yield       REAL NOT NULL DEFAULT 0,
	slippage_pct     REAL NOT NULL DEFAULT 0.0005
);

WITH RECURSIVE split(sym, rest) AS (
    SELECT '', list || ',' FROM symbol_lists WHERE symbol_list_id = 'etf-pre-2021-unleveraged'
    UNION ALL
    SELECT substr(rest, 1, instr(rest, ',') - 1), substr(rest, instr(rest, ',') + 1) FROM split WHERE rest <> ''
)
INSERT OR IGNORE INTO price_action_reclaim_strategy (id, name, symbol)
SELECT 'par-' || lower(sym), 'Price Action Reclaim ' || sym, sym FROM split WHERE sym <> '';
