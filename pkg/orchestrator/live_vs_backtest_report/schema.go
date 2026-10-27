package live_vs_backtest_report

// JobName is the job_runs.job value and the CLI subcommand.
const JobName = "live_vs_backtest_report"

// reportTables are the tables of one report build, kept in Turso. Each run has its own copy
// (see runtables), so a report can be audited after it was built. Only what is specific to the
// report lives here: the decisions and slippage it shows are read from live_vs_backtest_trade and
// live_vs_backtest_slippage, and nothing is copied from the account mirror or the market file.
var reportTables = []string{"live_vs_backtest_report_meta", "live_vs_backtest_report_note", "live_vs_backtest_report_round_trip"}

// keepRuns is how many builds' tables are kept in Turso.
const keepRuns = 3

const reportDDL = `
CREATE TABLE live_vs_backtest_report_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE live_vs_backtest_report_note (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	severity TEXT NOT NULL,
	message TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS live_vs_backtest_report_round_trip (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	account_number TEXT NOT NULL,
	strategy_id TEXT NOT NULL DEFAULT '',
	symbol TEXT NOT NULL,
	greenlit INTEGER NOT NULL DEFAULT 0,
	buy_order_id TEXT NOT NULL,
	sell_order_id TEXT NOT NULL DEFAULT '',
	buy_price REAL NOT NULL,
	sell_price REAL NOT NULL DEFAULT 0,
	qty REAL NOT NULL,
	fees REAL NOT NULL DEFAULT 0,
	cash_pnl REAL,
	return_pct REAL,
	opened_at TEXT NOT NULL DEFAULT '',
	closed_at TEXT NOT NULL DEFAULT '',
	manage_exit_id INTEGER NOT NULL DEFAULT 0,
	manage_status TEXT NOT NULL DEFAULT ''
);
`
