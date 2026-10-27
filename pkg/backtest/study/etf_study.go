package study

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// EtfStudy prepares the decline/streak slice and result-table schema for the
// "Top 5 S&P 500 ETFs 4-Day Position Study". Moved here from the auto-scanned
// sql/strategies/etf_study/ directory, where AutoRegisterSQLStrategies picked
// it up as a phantom "etf_study-sql" strategy even though it was never a
// backtestable strategy — it builds prep tables only, and this study was
// always incomplete: study_buy_signals is defined but nothing ever inserts
// into it (no RSI/SMA/etc. buy-signal rule was ever written), so running it
// produces the is_down_slice decline-streak analysis and empty result
// tables. Kept as-is rather than inventing a buy-signal rule that was never
// specified anywhere.
type EtfStudy struct {
	marketDBPath  string
	resultsDBPath string
}

func init() {
	Register(&EtfStudy{})
}

func (s *EtfStudy) ID() string { return "etf_study" }

func (s *EtfStudy) Name() string { return "Top 5 S&P 500 ETFs 4-Day Position Study" }

func (s *EtfStudy) Description() string {
	return "Builds a decline/streak analysis slice (is_down_slice) and the study_buy_signals/study_trades/" +
		"study_win_rates/study_horizon_comparison result-table schema. Incomplete: no buy-signal rule was " +
		"ever written, so study_buy_signals stays empty — this only produces the prep slice."
}

func (s *EtfStudy) SetDatabases(marketDBPath, resultsDBPath string) {
	s.marketDBPath = marketDBPath
	s.resultsDBPath = resultsDBPath
}

func (s *EtfStudy) Run() error {
	log.Printf("Running study: %s", s.Name())

	if err := os.MkdirAll(filepath.Dir(s.resultsDBPath), 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	db, err := sqlx.Open("sqlite", s.resultsDBPath)
	if err != nil {
		return fmt.Errorf("failed to open results db: %w", err)
	}
	defer db.Close()

	attachQuery := fmt.Sprintf("ATTACH DATABASE '%s' AS market;", s.marketDBPath)
	if _, err := db.Exec(attachQuery); err != nil {
		return fmt.Errorf("failed to attach market database: %w", err)
	}

	// Proxy backtest_start from the attached market DB, same as
	// SQLPipelineStrategy does for the sql/strategies/ pipelines this was
	// ported from.
	if _, err := db.Exec("CREATE TEMP VIEW IF NOT EXISTS backtest_start AS SELECT rowid, * FROM market.backtest_start;"); err != nil {
		return fmt.Errorf("failed to create backtest_start view: %w", err)
	}

	log.Println("Building is_down_slice (decline & streak analysis)...")
	if _, err := db.Exec(isDownSliceSQL); err != nil {
		return fmt.Errorf("failed to build is_down_slice: %w", err)
	}

	log.Println("Creating result-table schema (study_buy_signals, study_trades, study_win_rates, study_horizon_comparison)...")
	if _, err := db.Exec(resultSchemaSQL); err != nil {
		return fmt.Errorf("failed to create result schema: %w", err)
	}

	var count int
	_ = db.Get(&count, "SELECT COUNT(*) FROM is_down_slice;")
	log.Printf("Study execution complete: is_down_slice has %d rows. Results saved to: %s", count, s.resultsDBPath)
	log.Println("Note: study_buy_signals was never wired to a buy-signal rule and will be empty.")
	return nil
}

const isDownSliceSQL = `
DROP TABLE IF EXISTS is_down_slice;
CREATE TABLE is_down_slice AS
WITH step1 AS (
    SELECT
        symbol,
        substr(Date, 1, 10) AS date,
        close,
        LAG(close, 1) OVER (PARTITION BY symbol ORDER BY Date) AS prev_close,
        (close < LAG(close, 1) OVER (PARTITION BY symbol ORDER BY Date)) AS is_down
    FROM backtest_start
),
step2 AS (
    SELECT
        symbol,
        date,
        close,
        prev_close,
        is_down,
        ROW_NUMBER() OVER (PARTITION BY symbol ORDER BY date) -
        ROW_NUMBER() OVER (PARTITION BY symbol, is_down ORDER BY date) AS streak_id
    FROM step1
)
SELECT
    symbol,
    date,
    close,
    prev_close,
    is_down,
    streak_id,
    CASE
        WHEN is_down = 1 THEN COUNT(*) OVER (PARTITION BY symbol, is_down, streak_id)
        ELSE 0
    END AS streak_days
FROM step2;
CREATE INDEX IF NOT EXISTS idx_is_down_slice_sym_date ON is_down_slice(symbol, date);
`

const resultSchemaSQL = `
DROP TABLE IF EXISTS study_buy_signals;
CREATE TABLE IF NOT EXISTS study_buy_signals (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    idx INTEGER,
    symbol TEXT,
    date TEXT,
    open REAL,
    high REAL,
    low REAL,
    close REAL,
    volume BIGINT,
    rsi5 REAL,
    sma20 REAL,
    signal_type TEXT,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_study_signals_sym_date ON study_buy_signals(symbol, date);

DROP TABLE IF EXISTS study_trades;
CREATE TABLE IF NOT EXISTS study_trades (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    symbol TEXT,
    entry_idx INTEGER,
    entry_date TEXT,
    entry_price REAL,
    exit_idx INTEGER,
    exit_date TEXT,
    exit_price REAL,
    hold_days INTEGER,
    shares INTEGER,
    invested_capital REAL,
    gross_pnl REAL,
    net_pnl REAL,
    return_pct REAL,
    is_win INTEGER,
    mae_pct REAL,
    mfe_pct REAL,
    exit_reason TEXT DEFAULT '4_DAY_TIME_EXIT',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_study_trades_sym ON study_trades(symbol, entry_date);

DROP TABLE IF EXISTS study_win_rates;
CREATE TABLE IF NOT EXISTS study_win_rates (
    symbol TEXT PRIMARY KEY,
    total_trades INTEGER,
    winning_trades INTEGER,
    losing_trades INTEGER,
    win_rate REAL,
    win_rate_pct TEXT,
    profit_factor REAL,
    payoff_ratio REAL,
    avg_win_amount REAL,
    avg_loss_amount REAL,
    avg_trade_return_pct REAL,
    net_profit REAL,
    total_invested REAL,
    avg_mae_pct REAL,
    avg_mfe_pct REAL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

DROP TABLE IF EXISTS study_horizon_comparison;
CREATE TABLE IF NOT EXISTS study_horizon_comparison (
    hold_days INTEGER,
    total_trades INTEGER,
    winning_trades INTEGER,
    losing_trades INTEGER,
    win_rate REAL,
    win_rate_pct TEXT,
    profit_factor REAL,
    avg_trade_return_pct REAL,
    total_net_profit REAL,
    avg_mae_pct REAL,
    avg_mfe_pct REAL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`
