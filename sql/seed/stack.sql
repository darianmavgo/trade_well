-- Named stacks in refdata/strategies.db. id is generated ("stack-001") from num, label is the name
-- people type (under 30 characters, no '+' or ','), strategy_ids is the members joined with '+', primary first (a stack id).
-- Run: sqlite3 refdata/strategies.db < sql/seed/stack.sql
-- Backtest one with: backtest -strategy stack-001   (or its label)
CREATE TABLE IF NOT EXISTS stack (
	num          INTEGER PRIMARY KEY AUTOINCREMENT,
	id           TEXT GENERATED ALWAYS AS ('stack-' || printf('%03d', num)) VIRTUAL,
	label        TEXT NOT NULL UNIQUE COLLATE NOCASE
	             CHECK (length(label) BETWEEN 1 AND 29 AND label NOT GLOB '*[+,]*'),
	strategy_ids TEXT NOT NULL CHECK (length(trim(strategy_ids)) > 0 AND strategy_ids NOT GLOB '*,*'),
	note         TEXT NOT NULL DEFAULT '',
	created_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_stack_id ON stack(id);

INSERT OR REPLACE INTO stack (label, strategy_ids, note) VALUES (
	'Voo Streak Eleven',
	'streak-voo-buy-tecl+streak-roku-up3-roku+streak-ttd-up3-ttd+streak-sedg-down3-sedg+streak-googl-down3-googl+streak-avgo-down4-avgo+streak-pltr-down5-pltr+streak-tsla-up4-tsla+streak-se-down3-se+streak-riot-up4-riot+streak-ai-down5-ai',
	'VOO to TECL plus ten single-name streak sleeves; 82.7% CAGR, 19.8% max drawdown, 2021-2026 in-sample (shared_..._6.db). Was sig-voo-buy-tecl.');
