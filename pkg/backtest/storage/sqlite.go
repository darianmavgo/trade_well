package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	sqlfiles "github.com/darianmavgo/trade_well/sql"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

var validTableRegex = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// ValidateTableName ensures the table name contains only safe SQL identifier characters.
func ValidateTableName(name string) error {
	if name == "" {
		return nil
	}
	if !validTableRegex.MatchString(name) {
		return fmt.Errorf("invalid table name %q: must contain only alphanumeric characters and underscores", name)
	}
	return nil
}

// OpenSQLite opens or creates a SQLite database connection with optimized PRAGMAs.
func OpenSQLite(dbPath string) (*sqlx.DB, error) {
	if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}

	db, err := sqlx.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db at %s: %w", dbPath, err)
	}

	// Performance optimizations for local analytical backtesting
	_, _ = db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		PRAGMA temp_store = MEMORY;
		PRAGMA cache_size = -64000;
	`)

	return db, nil
}

// OpenSQLiteReadOnly opens an existing database without creating or changing
// it. A missing file is an error, not a new empty database.
func OpenSQLiteReadOnly(dbPath string) (*sqlx.DB, error) {
	if _, err := os.Stat(dbPath); err != nil {
		return nil, fmt.Errorf("sqlite db %s: %w", dbPath, err)
	}
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	db, err := sqlx.Open("sqlite", "file:"+abs+"?mode=ro&_busy_timeout=15000")
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite db at %s: %w", dbPath, err)
	}
	return db, nil
}

// ExecuteSQLFile reads and executes a SQL script file.
func ExecuteSQLFile(db *sqlx.DB, filePath string) error {
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("error reading %s: %w", filePath, err)
	}

	script := string(content)
	_, err = db.Exec(script)
	if err != nil {
		return fmt.Errorf("exec error in %s: %w", filepath.Base(filePath), err)
	}
	return nil
}

// EnsureBarTable creates the standard bar table schema if it does not exist.
func EnsureBarTable(db *sqlx.DB, tableName string) error {
	if tableName == "" {
		tableName = "backtest_start"
	}
	if err := ValidateTableName(tableName); err != nil {
		return err
	}
	schema := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			idx INTEGER,
			Date DATETIME,
			timeframe TEXT DEFAULT '1d',
			asset_class TEXT DEFAULT 'equity',
			open FLOAT,
			high FLOAT,
			low FLOAT,
			close FLOAT,
			"Adj Close" FLOAT,
			volume BIGINT,
			symbol TEXT
		);
		CREATE UNIQUE INDEX IF NOT EXISTS idx_%s_unique ON %s(symbol, Date, timeframe);
		CREATE INDEX IF NOT EXISTS idx_%s_sym_date ON %s(symbol, Date);
		CREATE INDEX IF NOT EXISTS idx_%s_sym_tf_date ON %s(symbol, timeframe, Date);
	`, tableName, tableName, tableName, tableName, tableName, tableName, tableName)
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	if tableName == "backtest_start" {
		// Market databases (daily, hourly, minute) all carry v_symbol_stats.
		view, err := sqlfiles.SymbolStatsView()
		if err != nil {
			return fmt.Errorf("read v_symbol_stats: %w", err)
		}
		if _, err := db.Exec(view); err != nil {
			return fmt.Errorf("create v_symbol_stats: %w", err)
		}
	}
	return nil
}

// SymbolDateCoverage contains the earliest date, latest date, and count of bars for a symbol in a table.
type SymbolDateCoverage struct {
	Symbol   string `db:"symbol"`
	MinDate  string `db:"min_date"`
	MaxDate  string `db:"max_date"`
	BarCount int    `db:"bar_count"`
}

// GetSymbolDateCoverage queries the existing date range and bar count for a symbol in tableName across all timeframes.
func GetSymbolDateCoverage(db *sqlx.DB, tableName, symbol string) (SymbolDateCoverage, error) {
	return GetSymbolDateCoverageWithTimeframe(db, tableName, symbol, "")
}

// GetSymbolDateCoverageWithTimeframe queries existing date coverage filtered by timeframe if provided.
func GetSymbolDateCoverageWithTimeframe(db *sqlx.DB, tableName, symbol, timeframe string) (SymbolDateCoverage, error) {
	cov := SymbolDateCoverage{Symbol: symbol}
	if tableName == "" {
		tableName = "backtest_start"
	}
	if err := ValidateTableName(tableName); err != nil {
		return cov, err
	}

	query := fmt.Sprintf(`
		SELECT 
			coalesce(MIN(substr(Date, 1, 10)), '') as min_date,
			coalesce(MAX(substr(Date, 1, 10)), '') as max_date,
			COUNT(*) as bar_count
		FROM %s
		WHERE symbol = ? AND (? = '' OR timeframe = ?)
	`, tableName)

	type row struct {
		MinDate  string `db:"min_date"`
		MaxDate  string `db:"max_date"`
		BarCount int    `db:"bar_count"`
	}
	var r row
	err := db.Get(&r, query, symbol, timeframe, timeframe)
	if err != nil {
		return cov, nil
	}
	cov.MinDate = r.MinDate
	cov.MaxDate = r.MaxDate
	cov.BarCount = r.BarCount
	return cov, nil
}

// UpsertBars inserts a slice of bars into the database using a transaction.
func UpsertBars(db *sqlx.DB, tableName string, bars []models.Bar) error {
	if len(bars) == 0 {
		return nil
	}
	if tableName == "" {
		tableName = "backtest_start"
	}
	if err := ValidateTableName(tableName); err != nil {
		return err
	}
	if err := EnsureBarTable(db, tableName); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	query := fmt.Sprintf(`
		INSERT OR REPLACE INTO %s (symbol, Date, timeframe, asset_class, open, high, low, close, "Adj Close", volume)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, tableName)
	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, b := range bars {
		tf := b.Timeframe
		if tf == "" {
			tf = "1d"
		}
		ac := b.AssetClass
		if ac == "" {
			ac = "equity"
		}
		adj := b.AdjClose
		if adj == 0 {
			adj = b.Close
		}
		_, err := stmt.Exec(b.Symbol, b.Date, tf, ac, b.Open, b.High, b.Low, b.Close, adj, b.Volume)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// FetchBars fetches bars with optional symbol filtering and date range.
func FetchBars(db *sqlx.DB, tableName string, symbols []string, startDate, endDate string) (map[string][]models.Bar, []string, error) {
	if tableName == "" {
		tableName = "backtest_start"
	}
	if err := ValidateTableName(tableName); err != nil {
		return nil, nil, err
	}

	query := fmt.Sprintf(`
		SELECT coalesce(idx, rowid, 0) as idx, symbol, substr(Date, 1, 10) as Date, open, high, low, close, volume,
		coalesce("Adj Close", 0) AS "Adj Close"
		FROM %s
		WHERE length(Date) = 10
	`, tableName)

	var args []interface{}
	if len(symbols) > 0 {
		placeholders := make([]string, len(symbols))
		for i, s := range symbols {
			placeholders[i] = "?"
			args = append(args, s)
		}
		query += fmt.Sprintf(" AND symbol IN (%s)", strings.Join(placeholders, ","))
	}
	// startDate is applied after the query (see below) so strategies still get
	// their pre-start warmup bars.
	if endDate != "" {
		query += " AND substr(Date, 1, 10) <= ?"
		args = append(args, endDate)
	}

	query += " ORDER BY Date ASC, symbol ASC;"

	var allBars []models.Bar
	err := db.Select(&allBars, query, args...)
	if err != nil {
		return nil, nil, err
	}

	bySymbol := make(map[string][]models.Bar)
	datesSeen := make(map[string]bool)
	var dates []string

	for _, b := range allBars {
		if startDate != "" && b.Date < startDate {
			continue
		}
		bySymbol[b.Symbol] = append(bySymbol[b.Symbol], b)
		if !datesSeen[b.Date] {
			datesSeen[b.Date] = true
			dates = append(dates, b.Date)
		}
	}

	return bySymbol, dates, nil
}

// FetchRecentBars retrieves only the most recent N bars per symbol for fast live scanning,
func FetchRecentBars(db *sqlx.DB, tableName string, symbols []string, limitPerSymbol int) (map[string][]models.Bar, []string, error) {
	if tableName == "" {
		tableName = "backtest_start"
	}
	if err := ValidateTableName(tableName); err != nil {
		return nil, nil, err
	}
	if limitPerSymbol <= 0 {
		limitPerSymbol = 250
	}

	whereClause := "WHERE length(Date) = 10"
	var args []interface{}
	if len(symbols) > 0 {
		placeholders := make([]string, len(symbols))
		for i, s := range symbols {
			placeholders[i] = "?"
			args = append(args, s)
		}
		whereClause += fmt.Sprintf(" AND symbol IN (%s)", strings.Join(placeholders, ","))
	}

	query := fmt.Sprintf(`
		SELECT idx, symbol, Date, open, high, low, close, volume
		FROM (
			SELECT 
				coalesce(idx, rowid, 0) as idx, 
				symbol, 
				substr(Date, 1, 10) as Date, 
				open, high, low, close, volume,
				ROW_NUMBER() OVER (PARTITION BY symbol ORDER BY substr(Date, 1, 10) DESC) as rn
			FROM %s
			%s
		)
		WHERE rn <= ?
		ORDER BY Date ASC, symbol ASC;
	`, tableName, whereClause)

	args = append(args, limitPerSymbol)

	var allBars []models.Bar
	err := db.Select(&allBars, query, args...)
	if err != nil {
		return nil, nil, err
	}

	bySymbol := make(map[string][]models.Bar)
	datesSeen := make(map[string]bool)
	var dates []string

	for _, b := range allBars {
		bySymbol[b.Symbol] = append(bySymbol[b.Symbol], b)
		if !datesSeen[b.Date] {
			datesSeen[b.Date] = true
			dates = append(dates, b.Date)
		}
	}

	return bySymbol, dates, nil
}

// FetchBenchmarkBars loads benchmark bars indexed by date (e.g. SPY).
func FetchBenchmarkBars(db *sqlx.DB, tableName, benchmarkSymbol string) (map[string]models.Bar, error) {
	if tableName == "" {
		tableName = "backtest_start"
	}
	if err := ValidateTableName(tableName); err != nil {
		return nil, err
	}
	if benchmarkSymbol == "" {
		benchmarkSymbol = "SPY"
	}

	query := fmt.Sprintf(`
		SELECT coalesce(idx, rowid, 0) as idx, symbol, substr(Date, 1, 10) as Date, open, high, low, close, volume
		FROM %s
		WHERE symbol = ?
		ORDER BY Date ASC;
	`, tableName)

	var bars []models.Bar
	err := db.Select(&bars, query, benchmarkSymbol)
	if err != nil {
		return nil, err
	}

	byDate := make(map[string]models.Bar)
	for _, b := range bars {
		byDate[b.Date] = b
	}
	return byDate, nil
}

// DefaultStartDate is the default backtest window start shared by the
// backtest, gridsearch and scoreboard commands (their -start flags).
const DefaultStartDate = "2021-01-01"

// FetchAllBarsChronological loads all historical bars indexed by symbol and date.
func FetchAllBarsChronological(db *sqlx.DB, tableName string) (map[string][]models.Bar, []string, error) {
	if tableName == "" {
		tableName = "backtest_start"
	}
	return FetchBars(db, tableName, nil, "", "")
}

// EnsureTradeTable creates the institutional trades table schema.
func EnsureTradeTable(db *sqlx.DB) error {
	schema := `
		CREATE TABLE IF NOT EXISTS trades (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			strategy_id TEXT,
			symbol TEXT,
			order_type TEXT,
			entry_idx INTEGER,
			entry_date TEXT,
			entry_price REAL,
			target_price REAL,
			stop_loss_price REAL,
			exit_date TEXT,
			exit_price REAL,
			exit_reason TEXT,
			shares INTEGER,
			invested_capital REAL,
			gross_pnl REAL,
			net_pnl REAL,
			return_pct REAL,
			hold_days INTEGER,
			commission_paid REAL,
			mae_pct REAL,
			mfe_pct REAL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_trades_strat_sym ON trades(strategy_id, symbol, entry_date);
	`
	_, err := db.Exec(schema)
	return err
}

func EnsureSignalTable(db *sqlx.DB) error {
	schema := `
		CREATE TABLE IF NOT EXISTS signals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			strategy_id TEXT,
			symbol TEXT,
			date TEXT,
			order_type TEXT,
			direction TEXT,
			entry_price REAL,
			take_profit REAL,
			stop_loss REAL,
			regime TEXT,
			metadata TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_signals_strat_sym ON signals(strategy_id, symbol, date);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// Backfill the column for signals.db files created before it existed — SQLite
	// has no "ADD COLUMN IF NOT EXISTS", so check first and ignore "duplicate
	// column" if another goroutine/run already added it.
	var hasMetadata bool
	rows, err := db.Query(`PRAGMA table_info(signals)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == "metadata" {
			hasMetadata = true
		}
	}
	if !hasMetadata {
		if _, err := db.Exec(`ALTER TABLE signals ADD COLUMN metadata TEXT`); err != nil {
			return err
		}
	}
	return nil
}

func SaveSignals(db *sqlx.DB, strategyID string, signals []models.Signal) error {
	if len(signals) == 0 {
		return nil
	}
	if err := EnsureSignalTable(db); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertSignals(tx, 0, strategyID, signals); err != nil {
		return err
	}
	return tx.Commit()
}

// insertSignals writes signals inside the caller's transaction. runID > 0
// tags the rows for the shared results database.
func insertSignals(tx Execer, runID int64, strategyID string, signals []models.Signal) error {
	cols, marks, lead := runCols(runID)
	query := `
		INSERT INTO signals (` + cols + `
			strategy_id, symbol, date, order_type, direction, entry_price, take_profit, stop_loss, regime, metadata
		) VALUES (` + marks + `?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, s := range signals {
		dir := s.Direction
		if dir == "" {
			dir = "LONG"
		}
		typ := s.OrderType
		if typ == "" {
			typ = "limit"
		}
		// In models.Signal, entry price is usually stored in BuyLimit or Close. BuyLimit is safer for Limit orders.
		entryPrice := s.BuyLimit
		if entryPrice == 0 {
			entryPrice = s.Close
		}

		stratID := strategyID
		if s.StrategyID != "" {
			stratID = s.StrategyID
		}

		var metadataJSON interface{}
		if len(s.Metadata) > 0 {
			b, err := json.Marshal(s.Metadata)
			if err != nil {
				return err
			}
			metadataJSON = string(b)
		}

		_, err := stmt.Exec(append(lead,
			stratID, s.Symbol, s.Date, typ, dir, entryPrice, s.TakeProfit, s.StopLoss, s.Regime, metadataJSON,
		)...)
		if err != nil {
			return err
		}
	}
	return nil
}

func EnsureEquityCurveTable(db *sqlx.DB) error {
	schema := `
		CREATE TABLE IF NOT EXISTS equity_curve (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			strategy_id TEXT,
			date TEXT,
			total_equity REAL,
			cash REAL,
			invested REAL,
			drawdown_pct REAL,
			buying_power REAL DEFAULT 0,
			margin_debt REAL DEFAULT 0,
			margin_interest REAL DEFAULT 0,
			dividend_income REAL DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_equity_strat_date ON equity_curve(strategy_id, date);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// Migrate existing tables if columns are missing
	for _, col := range []string{
		"ALTER TABLE equity_curve ADD COLUMN buying_power REAL DEFAULT 0",
		"ALTER TABLE equity_curve ADD COLUMN margin_debt REAL DEFAULT 0",
		"ALTER TABLE equity_curve ADD COLUMN margin_interest REAL DEFAULT 0",
		"ALTER TABLE equity_curve ADD COLUMN dividend_income REAL DEFAULT 0",
	} {
		_, _ = db.Exec(col)
	}
	return nil
}

func SaveEquityCurve(db *sqlx.DB, strategyID string, curve []models.DailyEquityPoint) error {
	if len(curve) == 0 {
		return nil
	}
	if err := EnsureEquityCurveTable(db); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertEquityCurve(tx, 0, strategyID, curve); err != nil {
		return err
	}
	return tx.Commit()
}

func insertEquityCurve(tx Execer, runID int64, strategyID string, curve []models.DailyEquityPoint) error {
	cols, marks, lead := runCols(runID)
	query := `
		INSERT INTO equity_curve (` + cols + `
			strategy_id, date, total_equity, cash, invested, drawdown_pct,
			buying_power, margin_debt, margin_interest, dividend_income
		) VALUES (` + marks + `?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range curve {
		_, err := stmt.Exec(append(lead, strategyID, p.Date, p.TotalEquity, p.Cash, p.PositionsValue, p.DrawdownPct,
			p.BuyingPower, p.MarginDebt, p.MarginInterest, p.DividendIncome)...)
		if err != nil {
			return err
		}
	}
	return nil
}

// SaveTrades persists completed simulation trades to the database.
func SaveTrades(db *sqlx.DB, strategyID string, trades []models.Trade) error {
	if len(trades) == 0 {
		return nil
	}
	if err := EnsureTradeTable(db); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := insertTrades(tx, 0, strategyID, trades); err != nil {
		return err
	}
	return tx.Commit()
}

func insertTrades(tx Execer, runID int64, strategyID string, trades []models.Trade) error {
	cols, marks, lead := runCols(runID)
	query := `
		INSERT INTO trades (` + cols + `
			strategy_id, symbol, order_type, entry_idx, entry_date, entry_price,
			target_price, stop_loss_price, exit_date, exit_price, exit_reason,
			shares, invested_capital, gross_pnl, net_pnl, return_pct, hold_days,
			commission_paid, mae_pct, mfe_pct
		) VALUES (` + marks + `?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	stmt, err := tx.Prepare(query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, t := range trades {
		stratID := strategyID
		if t.StrategyID != "" {
			stratID = t.StrategyID
		}

		_, err := stmt.Exec(append(lead,
			stratID, t.Symbol, t.OrderType, t.EntryIdx, t.EntryDate, t.EntryPrice,
			t.TargetPrice, t.StopLossPrice, t.ExitDate, t.ExitPrice, string(t.ExitReason),
			t.Shares, t.InvestedCapital, t.GrossPnL, t.NetPnL, t.ReturnPct, t.HoldDays,
			t.CommissionPaid, t.MaxAdverseExcursion, t.MaxFavorableExcursion,
		)...)
		if err != nil {
			return err
		}
	}

	return nil
}

// CreateUniqueDB generates an isolated SQLite file in dir with a filename matching baseName.
// If baseName.db already exists, it tries baseName_2.db, baseName_3.db, and so on.
// The file creation is atomic (using os.O_CREATE|os.O_EXCL) to prevent race conditions during concurrent backtests.
func CreateUniqueDB(dir, baseName string) (string, *sqlx.DB, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	cleanBase := strings.ToLower(strings.TrimSpace(baseName))
	cleanBase = strings.ReplaceAll(cleanBase, " ", "_")
	cleanBase = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, cleanBase)
	if cleanBase == "" {
		cleanBase = "backtest_results"
	}

	var targetPath string
	firstPath := filepath.Join(dir, fmt.Sprintf("%s.db", cleanBase))
	f, err := os.OpenFile(firstPath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0666)
	if err == nil {
		f.Close()
		targetPath = firstPath
	} else if os.IsExist(err) {
		for i := 2; ; i++ {
			candidate := filepath.Join(dir, fmt.Sprintf("%s_%d.db", cleanBase, i))
			f, err := os.OpenFile(candidate, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0666)
			if err == nil {
				f.Close()
				targetPath = candidate
				break
			}
			if !os.IsExist(err) {
				return "", nil, fmt.Errorf("failed to create candidate db file %s: %w", candidate, err)
			}
		}
	} else {
		return "", nil, fmt.Errorf("failed to create db file %s: %w", firstPath, err)
	}

	db, err := OpenSQLite(targetPath)
	if err != nil {
		return targetPath, nil, fmt.Errorf("failed to open newly created SQLite DB %s: %w", targetPath, err)
	}

	return targetPath, db, nil
}

// EnsurePerformanceReportTable creates the schema for saving quantitative backtest performance reports.
func EnsurePerformanceReportTable(db *sqlx.DB) error {
	schema := `
		CREATE TABLE IF NOT EXISTS performance_summary (
			strategy_id TEXT PRIMARY KEY,
			start_date TEXT,
			end_date TEXT,
			total_trading_days INTEGER,
			total_calendar_years REAL,
			initial_capital REAL,
			final_equity REAL,
			net_profit REAL,
			total_return_pct REAL,
			cagr REAL,
			sharpe_ratio REAL,
			sortino_ratio REAL,
			calmar_ratio REAL,
			omega_ratio REAL,
			ulcer_index REAL,
			alpha REAL,
			beta REAL,
			benchmark_return_pct REAL,
			max_drawdown_pct REAL,
			max_drawdown_dollars REAL,
			max_drawdown_peak_equity REAL,
			max_drawdown_trough_equity REAL,
			max_drawdown_peak_date TEXT,
			max_drawdown_trough_date TEXT,
			max_drawdown_days INTEGER,
			total_trades INTEGER,
			winning_trades INTEGER,
			losing_trades INTEGER,
			win_rate REAL,
			profit_factor REAL,
			avg_trade_return_pct REAL,
			avg_win_amount REAL,
			avg_loss_amount REAL,
			payoff_ratio REAL,
			avg_holding_days REAL,
			avg_mae REAL,
			avg_mfe REAL,
			total_commission_paid REAL,
			idle_days INTEGER,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	// Older result DBs predate idle_days. Duplicate-column errors are expected.
	_, _ = db.Exec(`ALTER TABLE performance_summary ADD COLUMN idle_days INTEGER;`)
	return nil
}

// SavePerformanceReport persists the quantitative performance summary tear sheet to SQLite.
func SavePerformanceReport(db *sqlx.DB, strategyID string, report models.PerformanceReport) error {
	if err := EnsurePerformanceReportTable(db); err != nil {
		return err
	}
	return insertPerformance(db, 0, strategyID, report)
}

func insertPerformance(db Execer, runID int64, strategyID string, report models.PerformanceReport) error {
	cols, marks, lead := runCols(runID)
	query := `
		INSERT OR REPLACE INTO performance_summary (` + cols + `
			strategy_id, start_date, end_date, total_trading_days, total_calendar_years,
			initial_capital, final_equity, net_profit, total_return_pct, cagr,
			sharpe_ratio, sortino_ratio, calmar_ratio, omega_ratio, ulcer_index,
			alpha, beta, benchmark_return_pct, max_drawdown_pct, max_drawdown_dollars,
			max_drawdown_peak_equity, max_drawdown_trough_equity,
			max_drawdown_peak_date, max_drawdown_trough_date, max_drawdown_days,
			total_trades, winning_trades, losing_trades, win_rate, profit_factor,
			avg_trade_return_pct, avg_win_amount, avg_loss_amount, payoff_ratio,
			avg_holding_days, avg_mae, avg_mfe, total_commission_paid, idle_days
		) VALUES (` + marks + `
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?,
			?, ?, ?,
			?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?
		);
	`
	var idleDays interface{}
	if report.IdleKnown {
		idleDays = report.IdleDays
	}
	_, err := db.Exec(query, append(lead,
		strategyID, report.StartDate, report.EndDate, report.TotalTradingDays, report.TotalCalendarYears,
		report.InitialCapital, report.FinalEquity, report.NetProfit, report.TotalReturnPct, report.CAGR,
		report.SharpeRatio, report.SortinoRatio, report.CalmarRatio, report.OmegaRatio, report.UlcerIndex,
		report.Alpha, report.Beta, report.BenchmarkReturnPct, report.MaxDrawdownPct, report.MaxDrawdownDollars,
		report.MaxDrawdownPeakEquity, report.MaxDrawdownTroughEquity,
		report.MaxDrawdownPeakDate, report.MaxDrawdownTroughDate, report.MaxDrawdownDuration,
		report.TotalTrades, report.WinningTrades, report.LosingTrades, report.WinRate, report.ProfitFactor,
		report.AvgTradeReturnPct, report.AvgWinAmount, report.AvgLossAmount, report.PayoffRatio,
		report.AvgHoldingDays, report.AvgMAE, report.AvgMFE, report.TotalCommissionPaid,
		idleDays)...,
	)
	return err
}

// FetchTradeSummaryStats computes core trade performance aggregates natively in SQLite.
func FetchTradeSummaryStats(db *sqlx.DB, strategyID string) (models.PerformanceReport, error) {
	var report models.PerformanceReport
	if err := EnsureTradeTable(db); err != nil {
		return report, err
	}

	query := `
		SELECT 
			COUNT(*) AS total_trades,
			COALESCE(SUM(CASE WHEN net_pnl > 0 THEN 1 ELSE 0 END), 0) AS winning_trades,
			COALESCE(SUM(CASE WHEN net_pnl <= 0 THEN 1 ELSE 0 END), 0) AS losing_trades,
			COALESCE(ROUND(CAST(SUM(CASE WHEN net_pnl > 0 THEN 1 ELSE 0 END) AS REAL) / NULLIF(COUNT(*), 0), 4), 0) AS win_rate,
			COALESCE(ROUND(SUM(net_pnl), 2), 0) AS net_profit,
			COALESCE(ROUND(SUM(commission_paid), 2), 0) AS total_commission_paid,
			COALESCE(ROUND(AVG(hold_days), 1), 0) AS avg_holding_days,
			COALESCE(ROUND(AVG(return_pct), 4), 0) AS avg_trade_return_pct,
			COALESCE(ROUND(AVG(CASE WHEN net_pnl > 0 THEN net_pnl ELSE NULL END), 2), 0) AS avg_win_amount,
			COALESCE(ROUND(AVG(CASE WHEN net_pnl < 0 THEN ABS(net_pnl) ELSE NULL END), 2), 0) AS avg_loss_amount,
			COALESCE(ROUND(SUM(CASE WHEN net_pnl > 0 THEN net_pnl ELSE 0 END) / 
				NULLIF(SUM(CASE WHEN net_pnl < 0 THEN ABS(net_pnl) ELSE 0 END), 0), 4), 0) AS profit_factor,
			COALESCE(ROUND(AVG(mae_pct), 4), 0) AS avg_mae,
			COALESCE(ROUND(AVG(mfe_pct), 4), 0) AS avg_mfe
		FROM trades
		WHERE strategy_id = ?;
	`
	type sqlStats struct {
		TotalTrades         int     `db:"total_trades"`
		WinningTrades       int     `db:"winning_trades"`
		LosingTrades        int     `db:"losing_trades"`
		WinRate             float64 `db:"win_rate"`
		NetProfit           float64 `db:"net_profit"`
		TotalCommissionPaid float64 `db:"total_commission_paid"`
		AvgHoldingDays      float64 `db:"avg_holding_days"`
		AvgTradeReturnPct   float64 `db:"avg_trade_return_pct"`
		AvgWinAmount        float64 `db:"avg_win_amount"`
		AvgLossAmount       float64 `db:"avg_loss_amount"`
		ProfitFactor        float64 `db:"profit_factor"`
		AvgMAE              float64 `db:"avg_mae"`
		AvgMFE              float64 `db:"avg_mfe"`
	}

	var stats sqlStats
	if err := db.Get(&stats, query, strategyID); err != nil {
		return report, err
	}

	report.TotalTrades = stats.TotalTrades
	report.WinningTrades = stats.WinningTrades
	report.LosingTrades = stats.LosingTrades
	report.WinRate = stats.WinRate
	report.NetProfit = stats.NetProfit
	report.TotalCommissionPaid = stats.TotalCommissionPaid
	report.AvgHoldingDays = stats.AvgHoldingDays
	report.AvgTradeReturnPct = stats.AvgTradeReturnPct
	report.AvgWinAmount = stats.AvgWinAmount
	report.AvgLossAmount = stats.AvgLossAmount
	report.ProfitFactor = stats.ProfitFactor
	report.AvgMAE = stats.AvgMAE
	report.AvgMFE = stats.AvgMFE

	if report.AvgLossAmount > 0 {
		report.PayoffRatio = report.AvgWinAmount / report.AvgLossAmount
	}

	return report, nil
}

// SaveReturnBreakdown records how a buy-and-hold total return splits into
// price appreciation and reinvested dividends, so it can be browsed in the
// strategy's result DB next to trades / equity_curve.
func SaveReturnBreakdown(db *sqlx.DB, strategyID, symbol, start, end string, reinvested bool, pricePct, dividendPct, totalPct float64) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS return_breakdown (
		strategy_id TEXT, symbol TEXT, start_date TEXT, end_date TEXT,
		dividends_reinvested INTEGER,
		price_return_pct REAL, dividend_return_pct REAL, total_return_pct REAL,
		PRIMARY KEY (strategy_id, symbol))`); err != nil {
		return err
	}
	return insertReturnBreakdown(db, 0, strategyID, symbol, start, end, reinvested, pricePct, dividendPct, totalPct)
}

func insertReturnBreakdown(db Execer, runID int64, strategyID, symbol, start, end string, reinvested bool, pricePct, dividendPct, totalPct float64) error {
	cols, marks, lead := runCols(runID)
	_, err := db.Exec(`INSERT OR REPLACE INTO return_breakdown (`+cols+`strategy_id, symbol, start_date, end_date,
		dividends_reinvested, price_return_pct, dividend_return_pct, total_return_pct) VALUES (`+marks+`?, ?, ?, ?, ?, ?, ?, ?)`,
		append(lead, strategyID, symbol, start, end, reinvested, pricePct, dividendPct, totalPct)...)
	return err
}

// RunMetric is one named number attached to a strategy run.
type RunMetric struct {
	Name  string
	Value float64
}

// SaveRunMetrics stores strategy-specific result numbers (e.g. dividends
// withdrawn, premium collected) in a run_metrics table next to trades.
func SaveRunMetrics(db *sqlx.DB, strategyID string, metrics []RunMetric) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS run_metrics (
		strategy_id TEXT, metric TEXT, value REAL, PRIMARY KEY (strategy_id, metric))`); err != nil {
		return err
	}
	return insertRunMetrics(db, 0, strategyID, metrics)
}

func insertRunMetrics(db Execer, runID int64, strategyID string, metrics []RunMetric) error {
	cols, marks, lead := runCols(runID)
	for _, m := range metrics {
		if _, err := db.Exec(`INSERT OR REPLACE INTO run_metrics (`+cols+`strategy_id, metric, value) VALUES (`+marks+`?, ?, ?)`,
			append(append([]any{}, lead...), strategyID, m.Name, m.Value)...); err != nil {
			return err
		}
	}
	return nil
}

// SharedAccountAudit holds the metadata for a shared account simulation.
type SharedAccountAudit struct {
	CombinedID          string  `db:"combined_id"`
	AccountModel        string  `db:"account_model"`
	PrimaryStrategyID   string  `db:"primary_strategy_id"`
	PrimaryStrategyName string  `db:"primary_strategy_name"`
	PositionSizePct     float64 `db:"position_size_pct"`
	PreemptedTrades     int     `db:"preempted_trades"`
	AvgIdleCashPct      float64 `db:"avg_idle_cash_pct"`
	FullyIdlePct        float64 `db:"fully_idle_pct"`
	AvgDeployedPct      float64 `db:"avg_deployed_pct"`
	ResultsDatabase     string  `db:"results_database"`
	DefaultAsset        string  `db:"default_asset"`
	AvgDefaultPct       float64 `db:"avg_default_pct"`
	DefaultDividends    float64 `db:"default_dividends"`
	DaysUnparked        int     `db:"days_unparked"`
}

// SharedAccountPriority holds the priority level of a strategy in a shared account run.
type SharedAccountPriority struct {
	CombinedID   string `db:"combined_id"`
	StrategyID   string `db:"strategy_id"`
	StrategyName string `db:"strategy_name"`
	Priority     int    `db:"priority"`
	Role         string `db:"role"`
}

// SaveSharedAccountAudit saves the shared account configuration and creates views to mimic the console audit.
func SaveSharedAccountAudit(db *sqlx.DB, audit SharedAccountAudit, priorities []SharedAccountPriority) error {
	schema := `
		CREATE TABLE IF NOT EXISTS shared_account_audit (
			combined_id TEXT PRIMARY KEY,
			account_model TEXT,
			primary_strategy_id TEXT,
			primary_strategy_name TEXT,
			position_size_pct FLOAT,
			preempted_trades INTEGER,
			avg_idle_cash_pct FLOAT,
			fully_idle_pct FLOAT,
			avg_deployed_pct FLOAT,
			results_database TEXT,
			default_asset TEXT,
			avg_default_pct FLOAT,
			default_dividends FLOAT,
			days_unparked INTEGER
		);
		CREATE TABLE IF NOT EXISTS shared_account_priorities (
			combined_id TEXT,
			strategy_id TEXT,
			strategy_name TEXT,
			priority INTEGER,
			role TEXT
		);
		
		CREATE VIEW IF NOT EXISTS v_strategy_contribution AS
		SELECT 
			p.role AS Role,
			p.strategy_id AS StrategyID,
			ps.total_trades AS TotalTrades,
			ps.winning_trades || ' / ' || ps.losing_trades AS Wins_Losses,
			ROUND(ps.win_rate * 100, 1) || '%%' AS WinRate,
			(SELECT COUNT(*) FROM trades t WHERE t.strategy_id = p.strategy_id AND t.exit_reason = 'PREEMPTED_BY_PRIMARY') AS Preempted,
			ROUND(ps.net_profit, 2) AS NetRealizedPnL,
			ROUND(ps.cagr * 100, 2) || '%%' AS CAGR,
			ROUND(ps.sharpe_ratio, 2) AS Sharpe,
			ROUND(ps.max_drawdown_pct * 100, 2) || '%%' AS MaxDD,
			ps.max_drawdown_duration || 'd' AS DDDuration
		FROM shared_account_priorities p
		JOIN performance_summary ps ON p.strategy_id = ps.strategy_id
		ORDER BY p.priority ASC;

		CREATE VIEW IF NOT EXISTS v_portfolio_tear_sheet AS
		SELECT * FROM performance_summary
		WHERE strategy_id NOT IN (SELECT strategy_id FROM shared_account_priorities);
	`
	if _, err := db.Exec(schema); err != nil {
		return fmt.Errorf("failed to create shared account audit tables/views: %w", err)
	}
	// Older result files created the table without the park columns.
	for _, stmt := range []string{
		`ALTER TABLE shared_account_audit ADD COLUMN default_asset TEXT`,
		`ALTER TABLE shared_account_audit ADD COLUMN avg_default_pct FLOAT`,
		`ALTER TABLE shared_account_audit ADD COLUMN default_dividends FLOAT`,
		`ALTER TABLE shared_account_audit ADD COLUMN days_unparked INTEGER`,
	} {
		_, _ = db.Exec(stmt)
	}

	return insertSharedAudit(db, 0, audit, priorities)
}

func insertSharedAudit(db namedExecer, runID int64, audit SharedAccountAudit, priorities []SharedAccountPriority) error {
	runCol, runMark := "", ""
	if runID > 0 {
		runCol, runMark = "run_id, ", ":run_id, "
	}
	type auditRow struct {
		RunID int64 `db:"run_id"`
		SharedAccountAudit
	}
	type priRow struct {
		RunID int64 `db:"run_id"`
		SharedAccountPriority
	}
	insertAudit := `
		INSERT OR REPLACE INTO shared_account_audit (` + runCol + `
			combined_id, account_model, primary_strategy_id, primary_strategy_name,
			position_size_pct, preempted_trades, avg_idle_cash_pct,
			fully_idle_pct, avg_deployed_pct, results_database,
			default_asset, avg_default_pct, default_dividends, days_unparked
		) VALUES (` + runMark + `
			:combined_id, :account_model, :primary_strategy_id, :primary_strategy_name,
			:position_size_pct, :preempted_trades, :avg_idle_cash_pct,
			:fully_idle_pct, :avg_deployed_pct, :results_database,
			:default_asset, :avg_default_pct, :default_dividends, :days_unparked
		)
	`
	if _, err := db.NamedExec(insertAudit, auditRow{runID, audit}); err != nil {
		return fmt.Errorf("failed to save shared_account_audit: %w", err)
	}

	insertPriority := `
		INSERT OR REPLACE INTO shared_account_priorities (` + runCol + `
			combined_id, strategy_id, strategy_name, priority, role
		) VALUES (` + runMark + `
			:combined_id, :strategy_id, :strategy_name, :priority, :role
		)
	`
	for _, p := range priorities {
		if _, err := db.NamedExec(insertPriority, priRow{runID, p}); err != nil {
			return fmt.Errorf("failed to save shared_account_priority %s: %w", p.StrategyID, err)
		}
	}

	return nil
}

// Execer is the part of *sqlx.DB and *sql.Tx the insert helpers need, so one
// implementation serves a per-file database and a transaction in results.db.
type Execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	Prepare(query string) (*sql.Stmt, error)
}

// namedExecer is the named-parameter side, satisfied by *sqlx.DB and *sqlx.Tx.
type namedExecer interface {
	NamedExec(query string, arg interface{}) (sql.Result, error)
}

// runCols returns the leading run_id column, placeholder and argument for a
// results.db insert. runID 0 means a legacy per-file table with no run_id.
func runCols(runID int64) (cols, marks string, lead []any) {
	if runID <= 0 {
		return "", "", nil
	}
	return "run_id, ", "?, ", []any{runID}
}

// RunStage executes every .sql file of the embedded stage sql/stages/<stage> in
// name order against db, after replacing each key of repl with its value. A
// stage file is split on its statement terminators, so a comment in one must not
// contain that character.
func RunStage(db *sqlx.DB, stage string, repl map[string]string) error {
	dir := path.Join("stages", stage)
	entries, err := fs.ReadDir(sqlfiles.FS, dir)
	if err != nil {
		return fmt.Errorf("stage %s: %w", stage, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		text, err := fs.ReadFile(sqlfiles.FS, path.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		sqlText := string(text)
		for k, v := range repl {
			sqlText = strings.ReplaceAll(sqlText, k, v)
		}
		for _, q := range strings.Split(sqlText, ";") {
			if q = strings.TrimSpace(q); q == "" {
				continue
			}
			if _, err := db.Exec(q); err != nil {
				return fmt.Errorf("stage %s %s: %w", stage, e.Name(), err)
			}
		}
	}
	return nil
}

// SymbolLastDate returns the date (YYYY-MM-DD) of the latest daily bar of symbol
// in the market database at path, or "" when it has none. The file is opened
// read-only.
func SymbolLastDate(path, symbol string) (string, error) {
	db, err := OpenSQLiteReadOnly(path)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var last sql.NullString
	if err := db.Get(&last, "SELECT MAX(substr(Date, 1, 10)) FROM backtest_start WHERE symbol = ? AND timeframe = '1d'", symbol); err != nil {
		return "", err
	}
	return last.String, nil
}

// DefaultHoldoutMonths is how much of the end of history is kept out of
// selection, ranking and tuning. backtest and gridsearch share it, so a sweep
// tunes on exactly the in-sample window the backtest reports.
const DefaultHoldoutMonths = 12

// minInSampleDays is the shortest in-sample window worth reporting.
const minInSampleDays = 365

// InSampleEnd returns the last in-sample date for a holdout of months before
// the newest daily bar (or before last, when non-empty). ok is false when the
// history from start is too short to split, in which case the caller uses all
// of it.
func InSampleEnd(dbPath, table, start, last string, months int) (end string, ok bool, err error) {
	if months <= 0 {
		return "", false, nil
	}
	if last == "" {
		db, err := OpenSQLite(dbPath)
		if err != nil {
			return "", false, err
		}
		defer db.Close()
		if err := ValidateTableName(table); err != nil {
			return "", false, err
		}
		q := fmt.Sprintf("SELECT COALESCE(MAX(substr(Date,1,10)),'') FROM %s WHERE length(Date) = 10", table)
		if err := db.Get(&last, q); err != nil {
			return "", false, err
		}
	}
	lastT, perr := time.Parse("2006-01-02", last)
	if perr != nil {
		return "", false, nil
	}
	cut := lastT.AddDate(0, -months, 0)
	if startT, perr := time.Parse("2006-01-02", start); start != "" && perr == nil && cut.Sub(startT) < minInSampleDays*24*time.Hour {
		return "", false, nil
	}
	return cut.Format("2006-01-02"), true, nil
}
