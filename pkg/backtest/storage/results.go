package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/jmoiron/sqlx"
)

// A backtest invocation is a run: data/reports/<run_id>/ (see NewRun). Inside it
// each strategy family has its own result database, <family>.db, and a held-out
// pass writes the same files under <run_id>/oos/. In a family file every strategy
// run is one row in runs, and the trades, signals, equity curve and summaries of
// that strategy run carry its (per file) run_id.

// ResultsDB is the shared result database. It is safe for concurrent writers:
// WAL lets readers run beside a writer, writers queue on busy_timeout, and each
// run is written in a single immediate transaction so a run is either wholly
// there or not there at all.
type ResultsDB struct {
	DB   *sqlx.DB
	Path string
}

// resultsPath is where a family's results live inside a run directory.
func resultsPath(dir, family string) string { return filepath.Join(dir, family+".db") }

// OpenResults opens (creating if needed) dir/<family>.db.
func OpenResults(dir, family string) (*ResultsDB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create results dir %s: %w", dir, err)
	}
	path := resultsPath(dir, family)
	dsn := path + "?_txlock=immediate&_pragma=busy_timeout(120000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=cache_size(-64000)"
	db, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	r := &ResultsDB{DB: db, Path: path}
	if err := r.ensureSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return r, nil
}

// Close releases the connection pool.
func (r *ResultsDB) Close() error { return r.DB.Close() }

const resultsSchema = `
CREATE TABLE IF NOT EXISTS runs (
	run_id          INTEGER PRIMARY KEY AUTOINCREMENT,
	strategy_id     TEXT NOT NULL,
	kind            TEXT NOT NULL DEFAULT 'single',
	window_start    TEXT,
	window_end      TEXT,
	capital         REAL,
	alloc_pct       REAL,
	default_asset   TEXT,
	market_max_date TEXT,
	engine          TEXT,
	suspect         TEXT,
	created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_runs_strategy ON runs(strategy_id, run_id);

CREATE TABLE IF NOT EXISTS signals (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL,
	strategy_id TEXT, symbol TEXT, date TEXT, order_type TEXT, direction TEXT,
	entry_price REAL, take_profit REAL, stop_loss REAL, regime TEXT, metadata TEXT,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_signals_run ON signals(run_id, strategy_id, symbol, date);

CREATE TABLE IF NOT EXISTS trades (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL,
	strategy_id TEXT, symbol TEXT, order_type TEXT, entry_idx INTEGER, entry_date TEXT,
	entry_price REAL, target_price REAL, stop_loss_price REAL, exit_date TEXT,
	exit_price REAL, exit_reason TEXT, shares INTEGER, invested_capital REAL,
	gross_pnl REAL, net_pnl REAL, return_pct REAL, hold_days INTEGER,
	commission_paid REAL, mae_pct REAL, mfe_pct REAL,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_trades_run ON trades(run_id, strategy_id, symbol, entry_date);

CREATE TABLE IF NOT EXISTS equity_curve (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id INTEGER NOT NULL,
	strategy_id TEXT, date TEXT, total_equity REAL, cash REAL, invested REAL,
	drawdown_pct REAL, buying_power REAL DEFAULT 0, margin_debt REAL DEFAULT 0,
	margin_interest REAL DEFAULT 0, dividend_income REAL DEFAULT 0,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_equity_run ON equity_curve(run_id, strategy_id, date);

CREATE TABLE IF NOT EXISTS performance_summary (
	run_id INTEGER NOT NULL,
	strategy_id TEXT NOT NULL,
	start_date TEXT, end_date TEXT, total_trading_days INTEGER, total_calendar_years REAL,
	initial_capital REAL, final_equity REAL, net_profit REAL, total_return_pct REAL, cagr REAL,
	sharpe_ratio REAL, sortino_ratio REAL, calmar_ratio REAL, omega_ratio REAL, ulcer_index REAL,
	alpha REAL, beta REAL, benchmark_return_pct REAL, max_drawdown_pct REAL, max_drawdown_dollars REAL,
	max_drawdown_peak_equity REAL, max_drawdown_trough_equity REAL,
	max_drawdown_peak_date TEXT, max_drawdown_trough_date TEXT, max_drawdown_days INTEGER,
	total_trades INTEGER, winning_trades INTEGER, losing_trades INTEGER, win_rate REAL, profit_factor REAL,
	avg_trade_return_pct REAL, avg_win_amount REAL, avg_loss_amount REAL, payoff_ratio REAL,
	avg_holding_days REAL, avg_mae REAL, avg_mfe REAL, total_commission_paid REAL, idle_days INTEGER,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (run_id, strategy_id)
);

CREATE TABLE IF NOT EXISTS return_breakdown (
	run_id INTEGER NOT NULL,
	strategy_id TEXT, symbol TEXT, start_date TEXT, end_date TEXT,
	dividends_reinvested INTEGER, price_return_pct REAL, dividend_return_pct REAL, total_return_pct REAL,
	PRIMARY KEY (run_id, strategy_id, symbol)
);

CREATE TABLE IF NOT EXISTS run_metrics (
	run_id INTEGER NOT NULL,
	strategy_id TEXT, metric TEXT, value REAL,
	PRIMARY KEY (run_id, strategy_id, metric)
);

CREATE TABLE IF NOT EXISTS shared_account_audit (
	run_id INTEGER NOT NULL,
	combined_id TEXT, account_model TEXT, primary_strategy_id TEXT, primary_strategy_name TEXT,
	position_size_pct FLOAT, preempted_trades INTEGER, avg_idle_cash_pct FLOAT,
	fully_idle_pct FLOAT, avg_deployed_pct FLOAT, results_database TEXT,
	default_asset TEXT, avg_default_pct FLOAT, default_dividends FLOAT, days_unparked INTEGER,
	PRIMARY KEY (run_id, combined_id)
);

CREATE TABLE IF NOT EXISTS shared_account_priorities (
	run_id INTEGER NOT NULL,
	combined_id TEXT, strategy_id TEXT, strategy_name TEXT, priority INTEGER, role TEXT,
	PRIMARY KEY (run_id, combined_id, strategy_id)
);

-- Latest finished run per strategy, with its headline numbers.
CREATE VIEW IF NOT EXISTS v_latest_runs AS
SELECT r.run_id, r.strategy_id, r.kind, r.window_start, r.window_end, r.created_at,
       p.cagr, p.max_drawdown_pct, p.calmar_ratio, p.sharpe_ratio, p.total_trades, p.final_equity
FROM runs r
JOIN performance_summary p ON p.run_id = r.run_id AND p.strategy_id = r.strategy_id
WHERE r.run_id = (SELECT MAX(r2.run_id) FROM runs r2 WHERE r2.strategy_id = r.strategy_id);
`

func (r *ResultsDB) ensureSchema() error {
	if _, err := r.DB.Exec(resultsSchema); err != nil {
		return fmt.Errorf("create results schema in %s: %w", r.Path, err)
	}
	// A results file made before these columns existed. A run with a NULL engine was made by the
	// daily simulator; a NULL suspect means nothing was found wrong with it.
	for _, col := range []string{"engine", "suspect"} {
		var has int
		if err := r.DB.Get(&has, `SELECT COUNT(*) FROM pragma_table_info('runs') WHERE name = ?`, col); err != nil {
			return err
		}
		if has == 0 {
			if _, err := r.DB.Exec(`ALTER TABLE runs ADD COLUMN ` + col + ` TEXT`); err != nil {
				return fmt.Errorf("add runs.%s in %s: %w", col, r.Path, err)
			}
		}
	}
	return nil
}

// The simulator that produced a run. A run with no engine recorded was made before the column
// existed, by the daily simulator, whose same-day fills and exits use the day's range.
const (
	EngineHourly = "hourly"
	EngineDaily  = "daily"
)

// RunMeta identifies a run. StrategyID is the strategy, or the a+b+c stack id.
type RunMeta struct {
	StrategyID    string
	Kind          string // "single" or "stack"
	WindowStart   string
	WindowEnd     string
	Capital       float64
	AllocPct      float64
	DefaultAsset  string
	MarketMaxDate string
	// Engine is EngineHourly. (EngineDaily marks runs from the deleted daily simulator; none is made now.)
	Engine string
	// Suspect says why the run should not be ranked (a traded symbol has an unadjusted split);
	// empty when nothing is wrong.
	Suspect string
}

// NamedReport is one performance_summary row of a run.
type NamedReport struct {
	StrategyID string
	Report     models.PerformanceReport
}

// RunPayload is everything a run writes. Nil or empty parts are skipped.
type RunPayload struct {
	Signals     []models.Signal
	Trades      []models.Trade
	Equity      []models.DailyEquityPoint
	Reports     []NamedReport // headline report first, then any sleeve reports
	Breakdown   *Breakdown
	Metrics     []RunMetric
	Audit       *SharedAccountAudit
	Priorities  []SharedAccountPriority
	TradesAsID  string // strategy_id for trades/signals/equity without their own
	EquityAsID  string // defaults to TradesAsID
	SignalsAsID string // defaults to TradesAsID
}

// Breakdown is the return_breakdown row of a total-return run.
type Breakdown struct {
	Symbol, Start, End string
	Reinvested         bool
	Price, Dividend    float64
	Total              float64
}

// WriteRun stores a run in one transaction and returns its run_id.
func (r *ResultsDB) WriteRun(meta RunMeta, p RunPayload) (int64, error) {
	tx, err := r.DB.Beginx()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO runs (strategy_id, kind, window_start, window_end, capital, alloc_pct, default_asset, market_max_date, engine, suspect)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		meta.StrategyID, meta.Kind, meta.WindowStart, meta.WindowEnd, meta.Capital, meta.AllocPct, meta.DefaultAsset, meta.MarketMaxDate, meta.Engine, meta.Suspect)
	if err != nil {
		return 0, err
	}
	runID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	id := p.TradesAsID
	if id == "" {
		id = meta.StrategyID
	}
	if len(p.Signals) > 0 {
		sid := orDefault(p.SignalsAsID, id)
		if err := insertSignals(tx, runID, sid, p.Signals); err != nil {
			return 0, err
		}
	}
	if len(p.Trades) > 0 {
		if err := insertTrades(tx, runID, id, p.Trades); err != nil {
			return 0, err
		}
	}
	if len(p.Equity) > 0 {
		if err := insertEquityCurve(tx, runID, orDefault(p.EquityAsID, id), p.Equity); err != nil {
			return 0, err
		}
	}
	for _, rep := range p.Reports {
		if err := insertPerformance(tx, runID, rep.StrategyID, rep.Report); err != nil {
			return 0, err
		}
	}
	if b := p.Breakdown; b != nil {
		if err := insertReturnBreakdown(tx, runID, meta.StrategyID, b.Symbol, b.Start, b.End, b.Reinvested, b.Price, b.Dividend, b.Total); err != nil {
			return 0, err
		}
	}
	if len(p.Metrics) > 0 {
		if err := insertRunMetrics(tx, runID, meta.StrategyID, p.Metrics); err != nil {
			return 0, err
		}
	}
	if p.Audit != nil {
		if err := insertSharedAudit(tx, runID, *p.Audit, p.Priorities); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return runID, nil
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

// LatestRun is the newest run of a strategy together with its headline report.
type LatestRun struct {
	RunID      int64
	StrategyID string
	CreatedAt  time.Time
	Report     models.PerformanceReport
	// Engine and Suspect are the run's runs.engine and runs.suspect (empty when not recorded).
	Engine, Suspect string
}

// LatestRuns returns the newest run per strategy id that has a summary row for
// the run's own strategy id (a stack run also stores a row per sleeve; those
// belong to the sleeve's standalone runs, not to the stack run).
func (r *ResultsDB) LatestRuns() ([]LatestRun, error) {
	rows, err := r.DB.Query(`
		SELECT r.run_id, r.strategy_id, r.created_at, COALESCE(r.engine, ''), COALESCE(r.suspect, ''),
			p.start_date, p.end_date, p.total_trading_days, p.total_calendar_years,
			p.initial_capital, p.final_equity, p.net_profit, p.total_return_pct, p.cagr,
			p.sharpe_ratio, p.sortino_ratio, p.calmar_ratio, p.omega_ratio, p.ulcer_index,
			p.alpha, p.beta, p.benchmark_return_pct, p.max_drawdown_pct, p.max_drawdown_dollars,
			p.max_drawdown_peak_equity, p.max_drawdown_trough_equity,
			p.max_drawdown_peak_date, p.max_drawdown_trough_date, p.max_drawdown_days,
			p.total_trades, p.winning_trades, p.losing_trades, p.win_rate, p.profit_factor,
			p.avg_trade_return_pct, p.avg_win_amount, p.avg_loss_amount, p.payoff_ratio,
			p.avg_holding_days, p.avg_mae, p.avg_mfe, p.total_commission_paid, p.idle_days,
			CASE WHEN p.idle_days IS NULL THEN (SELECT COUNT(*) FROM equity_curve e
				WHERE e.run_id = p.run_id AND e.strategy_id = p.strategy_id) END,
			CASE WHEN p.idle_days IS NULL THEN (SELECT COALESCE(SUM(CASE WHEN e.invested IS NULL OR e.invested = 0 THEN 1 ELSE 0 END), 0)
				FROM equity_curve e WHERE e.run_id = p.run_id AND e.strategy_id = p.strategy_id) END
		FROM runs r
		JOIN performance_summary p ON p.run_id = r.run_id AND p.strategy_id = r.strategy_id
		WHERE r.run_id = (SELECT MAX(r2.run_id) FROM runs r2
		                  JOIN performance_summary p2 ON p2.run_id = r2.run_id AND p2.strategy_id = r2.strategy_id
		                  WHERE r2.strategy_id = r.strategy_id)
		ORDER BY r.strategy_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LatestRun
	for rows.Next() {
		var lr LatestRun
		var created sql.NullTime
		var idle, sessions, idleCurve sql.NullInt64
		rep := &lr.Report
		if err := rows.Scan(&lr.RunID, &lr.StrategyID, &created, &lr.Engine, &lr.Suspect,
			&rep.StartDate, &rep.EndDate, &rep.TotalTradingDays, &rep.TotalCalendarYears,
			&rep.InitialCapital, &rep.FinalEquity, &rep.NetProfit, &rep.TotalReturnPct, &rep.CAGR,
			&rep.SharpeRatio, &rep.SortinoRatio, &rep.CalmarRatio, &rep.OmegaRatio, &rep.UlcerIndex,
			&rep.Alpha, &rep.Beta, &rep.BenchmarkReturnPct, &rep.MaxDrawdownPct, &rep.MaxDrawdownDollars,
			&rep.MaxDrawdownPeakEquity, &rep.MaxDrawdownTroughEquity,
			&rep.MaxDrawdownPeakDate, &rep.MaxDrawdownTroughDate, &rep.MaxDrawdownDuration,
			&rep.TotalTrades, &rep.WinningTrades, &rep.LosingTrades, &rep.WinRate, &rep.ProfitFactor,
			&rep.AvgTradeReturnPct, &rep.AvgWinAmount, &rep.AvgLossAmount, &rep.PayoffRatio,
			&rep.AvgHoldingDays, &rep.AvgMAE, &rep.AvgMFE, &rep.TotalCommissionPaid, &idle, &sessions, &idleCurve,
		); err != nil {
			return nil, fmt.Errorf("malformed performance_summary row: %w", err)
		}
		if created.Valid {
			lr.CreatedAt = created.Time
		}
		switch {
		case idle.Valid:
			rep.IdleDays = int(idle.Int64)
			rep.IdleKnown = true
		case sessions.Valid && sessions.Int64 > 0:
			rep.IdleDays = int(idleCurve.Int64)
			rep.IdleKnown = true
		}
		out = append(out, lr)
	}
	return out, rows.Err()
}

// IntegrityOK runs PRAGMA quick_check on the results file. A family file can be
// gigabytes, and the full integrity_check takes three times as long. Every run is
// one transaction, so a torn run cannot be there; this catches a damaged file.
func (r *ResultsDB) IntegrityOK() error {
	var res string
	if err := r.DB.QueryRow(`PRAGMA quick_check(1)`).Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return fmt.Errorf("quick_check failed: %s", res)
	}
	return nil
}

var (
	sharedMu sync.Mutex
	shared   = map[string]*ResultsDB{}
)

// ResultsFor returns the process-wide handle for the family's result database
// in a run directory, opening it on first use, so concurrent workers share one
// connection pool per family.
func ResultsFor(dir, family string) (*ResultsDB, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	key := resultsPath(abs, family)
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if r, ok := shared[key]; ok {
		return r, nil
	}
	r, err := OpenResults(abs, family)
	if err != nil {
		return nil, err
	}
	shared[key] = r
	return r, nil
}

// CloseSharedResults closes every handle ResultsFor opened.
func CloseSharedResults() {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	for k, r := range shared {
		_ = r.Close()
		delete(shared, k)
	}
}

// ResultFiles lists the family result databases in a run directory (the *.db
// files directly in it, not in oos/), by file name.
func ResultFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".db") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// runNumber parses a run directory name: a positive integer with no sign or spaces.
func runNumber(name string) (int, bool) {
	n, err := strconv.Atoi(name)
	if err != nil || n < 1 || strconv.Itoa(n) != name {
		return 0, false
	}
	return n, true
}

// LatestRunDir returns the highest numbered run directory under root, and its
// number. It reports 0 and "" when there is none.
func LatestRunDir(root string) (int, string) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, ""
	}
	best := 0
	for _, e := range entries {
		if n, ok := runNumber(e.Name()); ok && e.IsDir() && n > best {
			best = n
		}
	}
	if best == 0 {
		return 0, ""
	}
	return best, filepath.Join(root, strconv.Itoa(best))
}

// NewRun creates the next run directory under root, numbered one above the
// highest existing run, and returns the number and path. The directory is made
// with a plain mkdir, so two processes starting at once get different numbers.
func NewRun(root string) (int, string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 0, "", err
	}
	next, _ := LatestRunDir(root)
	for {
		next++
		dir := filepath.Join(root, strconv.Itoa(next))
		err := os.Mkdir(dir, 0o755)
		if err == nil {
			return next, dir, nil
		}
		if !os.IsExist(err) {
			return 0, "", err
		}
	}
}

// RunDir returns the directory of an existing run, or an error naming the runs there are.
func RunDir(root string, runID int) (string, error) {
	dir := filepath.Join(root, strconv.Itoa(runID))
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		latest, _ := LatestRunDir(root)
		return "", fmt.Errorf("no run %d in %s (latest is %d)", runID, root, latest)
	}
	return dir, nil
}

// RunFile returns the path of name inside a run folder under root, and the run's
// number. runID > 0 is that existing run. With runID 0, create makes the next
// run (the way a command that writes files starts one) and otherwise the latest
// run is used (the way a command that reads them finds the last one).
func RunFile(root string, runID int, create bool, name string) (string, int, error) {
	var dir string
	var err error
	switch {
	case runID > 0:
		dir, err = RunDir(root, runID)
	case create:
		runID, dir, err = NewRun(root)
	default:
		if runID, dir = LatestRunDir(root); dir == "" {
			err = fmt.Errorf("no runs in %s yet", root)
		}
	}
	if err != nil {
		return "", 0, err
	}
	return filepath.Join(dir, name), runID, nil
}
