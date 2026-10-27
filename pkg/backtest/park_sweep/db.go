// Package park_sweep runs every streak, Markov, and decision-tree row with
// leftover cash parked in one symbol. Inputs are the settings tables. Results
// live in one SQLite file.
package park_sweep

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

const schema = `
CREATE TABLE IF NOT EXISTS sweep_config (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	start_date TEXT NOT NULL,
	end_date TEXT NOT NULL,
	capital REAL NOT NULL,
	allocation_pct REAL,
	park_symbol TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS park_asset (
	symbol TEXT PRIMARY KEY,
	first_date TEXT,
	last_date TEXT,
	bar_count INTEGER,
	final_equity REAL,
	cagr REAL,
	max_drawdown_pct REAL,
	sharpe REAL,
	dividends REAL
);
CREATE TABLE IF NOT EXISTS sweep_strategy (
	strategy_id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	name TEXT NOT NULL,
	signal_symbol TEXT NOT NULL,
	trade_symbol TEXT NOT NULL,
	direction TEXT NOT NULL,
	signal_days INTEGER NOT NULL,
	hold_days INTEGER NOT NULL,
	take_profit_pct REAL NOT NULL,
	stop_loss_pct REAL NOT NULL,
	regime TEXT NOT NULL,
	target_state TEXT NOT NULL,
	allocation_pct REAL NOT NULL,
	cash_yield REAL NOT NULL,
	slippage_pct REAL NOT NULL,
	commission_per_share REAL NOT NULL,
	next_day_limit INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS strategy_run (
	strategy_id TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	error TEXT,
	final_equity REAL,
	cagr REAL,
	max_drawdown_pct REAL,
	sharpe REAL,
	total_trades INTEGER,
	winning_trades INTEGER,
	losing_trades INTEGER,
	win_rate REAL,
	idle_days INTEGER,
	avg_park_weight REAL,
	dividends REAL,
	sleeve_net REAL,
	park_contribution REAL,
	edge_vs_googl REAL,
	started_at TEXT,
	finished_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_strategy_run_status ON strategy_run(status);
`

// Config is the single sweep_config row.
type Config struct {
	StartDate     string          `db:"start_date"`
	EndDate       string          `db:"end_date"`
	Capital       float64         `db:"capital"`
	AllocationPct sql.NullFloat64 `db:"allocation_pct"`
	ParkSymbol    string          `db:"park_symbol"`
}

// Strategy is one runnable row copied out of the settings tables.
type Strategy struct {
	StrategyID         string  `db:"strategy_id"`
	Kind               string  `db:"kind"`
	Name               string  `db:"name"`
	SignalSymbol       string  `db:"signal_symbol"`
	TradeSymbol        string  `db:"trade_symbol"`
	Direction          string  `db:"direction"`
	SignalDays         int     `db:"signal_days"`
	HoldDays           int     `db:"hold_days"`
	TakeProfitPct      float64 `db:"take_profit_pct"`
	StopLossPct        float64 `db:"stop_loss_pct"`
	Regime             string  `db:"regime"`
	TargetState        string  `db:"target_state"`
	AllocationPct      float64 `db:"allocation_pct"`
	CashYield          float64 `db:"cash_yield"`
	SlippagePct        float64 `db:"slippage_pct"`
	CommissionPerShare float64 `db:"commission_per_share"`
	NextDayLimit       int     `db:"next_day_limit"`
}

func Open(path string) (*sqlx.DB, error) {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("park_sweep schema: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout = 30000`); err != nil {
		db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func ensureConfig(db *sqlx.DB) error {
	_, err := db.Exec(`
		INSERT INTO sweep_config (id, start_date, end_date, capital, allocation_pct, park_symbol)
		VALUES (1, '2021-10-01', '2026-10-01', 100000, NULL, 'GOOGL')
		ON CONFLICT(id) DO NOTHING`)
	return err
}

func loadConfig(db *sqlx.DB) (Config, error) {
	var cfg Config
	err := db.Get(&cfg, `
		SELECT start_date, end_date, capital, allocation_pct, park_symbol
		FROM sweep_config WHERE id = 1`)
	if err != nil {
		return cfg, fmt.Errorf("sweep_config: %w", err)
	}
	cfg.ParkSymbol = strings.ToUpper(strings.TrimSpace(cfg.ParkSymbol))
	if cfg.ParkSymbol == "" {
		return cfg, fmt.Errorf("sweep_config.park_symbol is empty")
	}
	if cfg.Capital <= 0 {
		return cfg, fmt.Errorf("sweep_config.capital must be positive")
	}
	if cfg.StartDate == "" || cfg.EndDate == "" || cfg.StartDate > cfg.EndDate {
		return cfg, fmt.Errorf("sweep_config dates %s .. %s", cfg.StartDate, cfg.EndDate)
	}
	return cfg, nil
}

func openReadOnly(path string) (*sqlx.DB, error) {
	db, err := sqlx.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	return db, nil
}
