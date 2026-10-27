-- Named stacks in refdata/strategies.db. name is the friendly label, id the
-- members joined with "+". Run: sqlite3 refdata/strategies.db < sql/seed/stack.sql
CREATE TABLE IF NOT EXISTS stack (
	name TEXT PRIMARY KEY,
	id   TEXT NOT NULL UNIQUE,
	note TEXT NOT NULL DEFAULT ''
);
INSERT OR REPLACE INTO stack (name, id, note) VALUES (
	'Voo Streak Eleven',
	'streak-voo-buy-tecl+streak-roku-up3-roku+streak-ttd-up3-ttd+streak-sedg-down3-sedg+streak-googl-down3-googl+streak-avgo-down4-avgo+streak-pltr-down5-pltr+streak-tsla-up4-tsla+streak-se-down3-se+streak-riot-up4-riot+streak-ai-down5-ai',
	'VOO to TECL plus ten single-name streak sleeves; 82.7% CAGR, 19.8% max drawdown, 2021-2026 in-sample (shared_..._6.db). Was sig-voo-buy-tecl.');
