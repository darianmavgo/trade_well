-- Statements of pkg/orchestrator/live_vs_backtest_trades, read with orchestratorsql.Q("live_vs_backtest_trades/<name>").
-- Each starts with a "-- name:" line.

-- name: backfill_fills
UPDATE manage_exit SET entry_fill_price = ?, filled_qty = CASE WHEN ? > 0 THEN ? ELSE filled_qty END
				WHERE id = ? AND entry_fill_price = 0

-- name: backfill_fills_2
UPDATE manage_exit SET exit_fill_price = ? WHERE id = ? AND exit_fill_price = 0

-- name: backfill_fills_3
UPDATE manage_exit SET stage_order_id = (
		SELECT id FROM stage_order WHERE order_id = manage_exit.entry_order_id ORDER BY id DESC LIMIT 1
	) WHERE stage_order_id = 0 AND entry_order_id != ''
		AND EXISTS (SELECT 1 FROM stage_order WHERE order_id = manage_exit.entry_order_id)

-- name: backfill_fills_4
UPDATE manage_exit SET
		buy_signal_id = COALESCE((SELECT id FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), 0),
		strategy_id = CASE WHEN strategy_id = '' THEN COALESCE((SELECT strategy_id FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), strategy_name) ELSE strategy_id END,
		as_of_date = CASE WHEN as_of_date = '' THEN COALESCE((SELECT as_of_date FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), '') ELSE as_of_date END,
		next_session = CASE WHEN next_session = '' THEN COALESCE((SELECT next_session FROM scan_market_buy_signal b WHERE b.staged_order_id = manage_exit.stage_order_id ORDER BY id DESC LIMIT 1), '') ELSE next_session END
		WHERE stage_order_id > 0 AND (buy_signal_id = 0 OR strategy_id = '' OR as_of_date = '')

-- name: ensure_schema
CREATE TABLE IF NOT EXISTS live_vs_backtest_trade (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	account_number TEXT NOT NULL DEFAULT '',
	strategy_id TEXT NOT NULL,
	symbol TEXT NOT NULL,
	as_of_date TEXT NOT NULL,
	next_session TEXT NOT NULL DEFAULT '',
	buy_signal_id INTEGER NOT NULL DEFAULT 0,
	signal_id INTEGER NOT NULL DEFAULT 0,
	stage_order_id INTEGER NOT NULL DEFAULT 0,
	manage_exit_id INTEGER NOT NULL DEFAULT 0,
	entry_order_id TEXT NOT NULL DEFAULT '',
	live_outcome TEXT NOT NULL,
	skip_reason TEXT NOT NULL DEFAULT '',
	entry_limit REAL NOT NULL DEFAULT 0,
	entry_fill_price REAL NOT NULL DEFAULT 0,
	exit_fill_price REAL NOT NULL DEFAULT 0,
	filled_qty REAL NOT NULL DEFAULT 0,
	fees REAL NOT NULL DEFAULT 0,
	live_exit_date TEXT NOT NULL DEFAULT '',
	live_exit_reason TEXT NOT NULL DEFAULT '',
	live_return_pct REAL NOT NULL DEFAULT 0,
	model_outcome TEXT NOT NULL DEFAULT '',
	model_entry_price REAL NOT NULL DEFAULT 0,
	model_exit_price REAL NOT NULL DEFAULT 0,
	model_exit_date TEXT NOT NULL DEFAULT '',
	model_exit_reason TEXT NOT NULL DEFAULT '',
	model_return_pct REAL NOT NULL DEFAULT 0,
	entry_slippage_bps REAL,
	exit_slippage_bps REAL,
	entry_vs_limit_bps REAL,
	fill_delay_sessions REAL,
	signal_match INTEGER NOT NULL DEFAULT 0,
	outcome_match INTEGER NOT NULL DEFAULT 0,
	built_at TEXT NOT NULL,
	UNIQUE(account_number, strategy_id, symbol, as_of_date)
);
CREATE INDEX IF NOT EXISTS idx_live_vs_backtest_trade_strategy ON live_vs_backtest_trade(strategy_id, as_of_date);

-- name: load_exits
SELECT id, account_number, symbol, strategy_name,
		COALESCE(strategy_id, ''), COALESCE(as_of_date, ''), COALESCE(next_session, ''),
		COALESCE(signal_id, 0), COALESCE(buy_signal_id, 0), COALESCE(stage_order_id, 0),
		COALESCE(entry_order_id, ''),
		COALESCE(entry_price, 0), COALESCE(entry_fill_price, 0), COALESCE(exit_fill_price, 0),
		COALESCE(exit_price, 0), COALESCE(filled_qty, 0), COALESCE(fees, 0), COALESCE(quantity, 0),
		status, COALESCE(exit_reason, ''), COALESCE(exit_reason_code, ''), COALESCE(exit_date, ''),
		COALESCE(pnl_pct, 0), entry_time, entry_filled_at, COALESCE(max_trading_days, 0)
		FROM manage_exit

-- name: load_signals
SELECT id, strategy_id, symbol, as_of_date, next_session,
		close, buy_limit, take_profit, stop_loss, hold_days, status,
		COALESCE(notes, ''), COALESCE(staged_order_id, 0)
		FROM scan_market_buy_signal

-- name: load_stages
SELECT id, COALESCE(signal_id, 0), account_number, strategy_name, symbol,
		quantity, entry_price, take_profit_price, stop_loss_price, max_trading_days, status,
		COALESCE(reject_reason, ''), COALESCE(order_id, ''), staged_at
		FROM stage_order

-- name: save
DELETE FROM live_vs_backtest_trade

-- name: save_2
DELETE FROM live_vs_backtest_trade WHERE account_number = ? OR account_number = ''

-- name: save_3
INSERT INTO live_vs_backtest_trade (
		account_number, strategy_id, symbol, as_of_date, next_session,
		buy_signal_id, signal_id, stage_order_id, manage_exit_id, entry_order_id,
		live_outcome, skip_reason, entry_limit, entry_fill_price, exit_fill_price,
		filled_qty, fees, live_exit_date, live_exit_reason, live_return_pct,
		model_outcome, model_entry_price, model_exit_price, model_exit_date, model_exit_reason, model_return_pct,
		entry_slippage_bps, exit_slippage_bps, entry_vs_limit_bps, fill_delay_sessions,
		signal_match, outcome_match, built_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
