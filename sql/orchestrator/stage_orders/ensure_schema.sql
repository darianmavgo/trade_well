CREATE TABLE IF NOT EXISTS stage_order (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	signal_id INTEGER,
	account_number TEXT NOT NULL,
	strategy_name TEXT NOT NULL,
	symbol TEXT NOT NULL,
	instruction TEXT NOT NULL DEFAULT 'BUY',
	quantity REAL NOT NULL,
	entry_price REAL NOT NULL,
	take_profit_price REAL NOT NULL,
	stop_loss_price REAL NOT NULL,
	max_trading_days INTEGER NOT NULL DEFAULT 8,
	status TEXT NOT NULL DEFAULT 'PENDING',
	reject_reason TEXT,
	order_id TEXT,
	client_order_id TEXT,
	staged_at DATETIME NOT NULL,
	submitted_at DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_stage_order_client_order_id ON stage_order(client_order_id) WHERE client_order_id IS NOT NULL AND client_order_id != '';
