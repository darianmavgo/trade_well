-- The first two hmm strategies. Both trade QQQ from the QQQ hidden Markov model (train hmm).
-- The regimes are volatility regimes, so a rule uses the stressed regime and never a state number.
--   hmm-calm-qqq:     long while the stressed probability is under 50%. Hold 3 sessions then re-check.
--   hmm-calmdown-qqq: long when the stressed probability falls back under 50%. Hold 10 sessions.
--   hmm-calm-soxl:    the QQQ model's calm rule, traded on SOXL (3x semiconductors). Stop is wider because SOXL moves about 3x as much.
--   hmm-calm-tqqq:   the same rule on TQQQ (3x Nasdaq-100), same exits as hmm-calm-soxl.
--   hmm-normal-nrgu / -usd / -tecl: long while the QQQ model's most likely regime is the middle (normal) one,
--     traded on NRGU (3x oil), USD (2x semiconductors) and TECL (3x technology). stress_prob is unused by this rule.
-- Exits are starting points, not tuned. Needs `train hmm` and the daily bars (daily_bars).
-- Run: sqlite3 refdata/strategies.db < sql/seed/hmm_strategy.sql
CREATE TABLE IF NOT EXISTS hmm_strategy (
	id               TEXT PRIMARY KEY,
	name             TEXT NOT NULL,
	hmm_symbol       TEXT NOT NULL DEFAULT 'QQQ',
	trade_symbol     TEXT NOT NULL DEFAULT 'QQQ',
	rule             TEXT NOT NULL,
	stress_prob      REAL NOT NULL,
	hold_days        INTEGER NOT NULL,
	take_profit_pct  REAL NOT NULL,
	stop_loss_pct    REAL NOT NULL,
	allocation_pct   REAL NOT NULL,
	cash_yield       REAL NOT NULL,
	slippage_pct     REAL NOT NULL,
	source_strategy  TEXT,
	source_label     TEXT,
	win_rate         REAL,
	total_trades     INTEGER
);

INSERT OR REPLACE INTO hmm_strategy
    (id, name, hmm_symbol, trade_symbol, rule, stress_prob, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct)
VALUES
    ('hmm-calm-qqq', 'QQQ Long While Calm (HMM)', 'QQQ', 'QQQ', 'calm_filter', 0.5, 3, 0.0, 0.04, 0.20, 0.045, 0.0005),
    ('hmm-calmdown-qqq', 'QQQ Long After Stress Eases (HMM)', 'QQQ', 'QQQ', 'calm_down', 0.5, 10, 0.0, 0.05, 0.20, 0.045, 0.0005),
    ('hmm-calm-soxl', 'SOXL Long While QQQ Calm (HMM)', 'QQQ', 'SOXL', 'calm_filter', 0.5, 3, 0.0, 0.12, 0.20, 0.045, 0.0005),
    ('hmm-calm-tqqq', 'TQQQ Long While QQQ Calm (HMM)', 'QQQ', 'TQQQ', 'calm_filter', 0.5, 3, 0.0, 0.12, 0.20, 0.045, 0.0005),
    ('hmm-normal-nrgu', 'NRGU Long While QQQ Normal (HMM)', 'QQQ', 'NRGU', 'normal_filter', 0.5, 3, 0.0, 0.12, 0.20, 0.045, 0.0005),
    ('hmm-normal-usd', 'USD Long While QQQ Normal (HMM)', 'QQQ', 'USD', 'normal_filter', 0.5, 3, 0.0, 0.08, 0.20, 0.045, 0.0005),
    ('hmm-normal-tecl', 'TECL Long While QQQ Normal (HMM)', 'QQQ', 'TECL', 'normal_filter', 0.5, 3, 0.0, 0.12, 0.20, 0.045, 0.0005);
