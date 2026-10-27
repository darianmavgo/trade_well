package walk_forward

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/hoursim"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	sqlfiles "github.com/darianmavgo/trade_well/sql"
)

// FoldRow is one strategy's in-sample and out-of-sample result for one fold.
// Returns are fractions, the same unit the simulator reports.
type FoldRow struct {
	StrategyID   string
	Fold         int
	ISStart      string
	ISEnd        string
	OOSStart     string
	OOSEnd       string
	ISSharpe     float64
	OOSSharpe    float64
	ISReturnPct  float64
	OOSReturnPct float64
	ISTrades     int
	OOSTrades    int
	ISMaxDD      float64
	OOSMaxDD     float64
	// Idle days are the sessions with no open position, out of the sessions of the
	// window. Zero sessions means the window was empty.
	ISIdleDays  int
	ISDays      int
	OOSIdleDays int
	OOSDays     int
	Trials      int
}

// Options controls one walk-forward run. Zero months fall back to 24/6/6.
type Options struct {
	TrainMonths int
	TestMonths  int
	StepMonths  int
	Capital     float64
	// Hourly are the symbols' hourly bars (Eastern "YYYY-MM-DD HH:MM", regular hours), so fills and
	// exits are judged hour by hour. Nil plays each session as its daily bar.
	Hourly map[string][]models.Bar
}

func (o Options) norm() Options {
	if o.TrainMonths < 1 {
		o.TrainMonths = 24
	}
	if o.TestMonths < 1 {
		o.TestMonths = 6
	}
	if o.StepMonths < 1 {
		o.StepMonths = 6
	}
	if o.Capital <= 0 {
		o.Capital = 100000
	}
	return o
}

// TrialCount is the hold x take-profit x stop grid strateval would search.
// A strategy with no declared ranges counts as one trial, its default config.
func TrialCount(s strategy.Strategy) int {
	sp := strategy.AssessParameterSpace(s)
	n := len(sp.HoldDays) * len(sp.TakeProfits) * len(sp.StopLosses)
	if n < 1 {
		return 1
	}
	return n
}

// RunStrategy walks strat across dates. bars must already be the bars
// GenerateSignals should see (indicator warmup included). Each fold simulates
// only its own window, starting flat.
func RunStrategy(ctx context.Context, strat strategy.Strategy, bars map[string][]models.Bar, dates []string, opt Options) ([]FoldRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	opt = opt.norm()
	bars, dates = normalizeBars(bars)
	folds, err := Folds(dates, opt.TrainMonths, opt.TestMonths, opt.StepMonths)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", strat.ID(), err)
	}
	signals := strat.GenerateSignals(bars)
	cfg := strat.DefaultConfig()
	trials := TrialCount(strat)
	rows := make([]FoldRow, 0, len(folds))
	for _, f := range folds {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		isRep, err := simulate(cfg, signals, bars, opt.Hourly, between(dates, f.ISStart, f.ISEnd), opt.Capital)
		if err != nil {
			return nil, err
		}
		oosRep, err := simulate(cfg, signals, bars, opt.Hourly, between(dates, f.OOSStart, f.OOSEnd), opt.Capital)
		if err != nil {
			return nil, err
		}
		rows = append(rows, FoldRow{
			StrategyID: strat.ID(), Fold: f.Index,
			ISStart: f.ISStart, ISEnd: f.ISEnd, OOSStart: f.OOSStart, OOSEnd: f.OOSEnd,
			ISSharpe: isRep.SharpeRatio, OOSSharpe: oosRep.SharpeRatio,
			ISReturnPct: isRep.TotalReturnPct, OOSReturnPct: oosRep.TotalReturnPct,
			ISTrades: isRep.TotalTrades, OOSTrades: oosRep.TotalTrades,
			ISMaxDD: isRep.MaxDrawdownPct, OOSMaxDD: oosRep.MaxDrawdownPct,
			ISIdleDays: isRep.IdleDays, ISDays: isRep.TotalTradingDays,
			OOSIdleDays: oosRep.IdleDays, OOSDays: oosRep.TotalTradingDays,
			Trials: trials,
		})
	}
	return rows, nil
}

// simulate plays one window on the hourly simulator. A strategy that uses a setting it does not
// model is an error naming the setting.
func simulate(cfg strategy.StrategyConfig, signals []models.Signal, bars, hourly map[string][]models.Bar, dates []string, capital float64) (models.PerformanceReport, error) {
	if len(dates) == 0 {
		return models.PerformanceReport{}, nil
	}
	out, err := hoursim.Run(hoursim.Input{Signals: signals, Daily: bars, Hourly: hourly, Dates: dates, Config: cfg, Capital: capital})
	if err != nil {
		return models.PerformanceReport{}, fmt.Errorf("%s: %w", cfg.ID, err)
	}
	return out.Report, nil
}

func between(dates []string, start, end string) []string {
	out := make([]string, 0, len(dates))
	for _, d := range dates {
		if d >= start && d <= end {
			out = append(out, d)
		}
	}
	return out
}

func normalizeBars(in map[string][]models.Bar) (map[string][]models.Bar, []string) {
	out := make(map[string][]models.Bar, len(in))
	seen := map[string]bool{}
	var dates []string
	for sym, bars := range in {
		cp := make([]models.Bar, len(bars))
		for i, b := range bars {
			if len(b.Date) >= 10 {
				b.Date = b.Date[:10]
			}
			cp[i] = b
			if !seen[b.Date] {
				seen[b.Date] = true
				dates = append(dates, b.Date)
			}
		}
		out[sym] = cp
	}
	sort.Strings(dates)
	return out, dates
}

// EnsureSchema creates the fold table. The summary table is rebuilt by Summarize.
func EnsureSchema(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS walk_forward_fold (
	strategy_id TEXT NOT NULL,
	fold INTEGER NOT NULL,
	is_start TEXT NOT NULL,
	is_end TEXT NOT NULL,
	oos_start TEXT NOT NULL,
	oos_end TEXT NOT NULL,
	is_sharpe REAL NOT NULL,
	oos_sharpe REAL NOT NULL,
	is_return_pct REAL NOT NULL,
	oos_return_pct REAL NOT NULL,
	is_trades INTEGER NOT NULL,
	oos_trades INTEGER NOT NULL,
	is_max_dd REAL NOT NULL,
	oos_max_dd REAL NOT NULL,
	trials INTEGER NOT NULL DEFAULT 1,
	is_idle_days INTEGER NOT NULL DEFAULT 0,
	is_days INTEGER NOT NULL DEFAULT 0,
	oos_idle_days INTEGER NOT NULL DEFAULT 0,
	oos_days INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (strategy_id, fold)
)`)
	if err != nil {
		return err
	}
	// A fold table made before idle days existed gets the columns; the folds already in
	// it read as 0 of 0 sessions until the strategy is run again.
	for _, col := range []string{"is_idle_days", "is_days", "oos_idle_days", "oos_days"} {
		var have int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('walk_forward_fold') WHERE name = ?`, col).Scan(&have); err != nil {
			return err
		}
		if have == 0 {
			if _, err := db.Exec(`ALTER TABLE walk_forward_fold ADD COLUMN ` + col + ` INTEGER NOT NULL DEFAULT 0`); err != nil {
				return err
			}
		}
	}
	return nil
}

// Save replaces one strategy's folds and rebuilds walk_forward_summary.
func Save(db *sql.DB, strategyID string, rows []FoldRow) error {
	if err := EnsureSchema(db); err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM walk_forward_fold WHERE strategy_id = ?`, strategyID); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := tx.Exec(`INSERT INTO walk_forward_fold (
			strategy_id, fold, is_start, is_end, oos_start, oos_end,
			is_sharpe, oos_sharpe, is_return_pct, oos_return_pct,
			is_trades, oos_trades, is_max_dd, oos_max_dd, trials,
			is_idle_days, is_days, oos_idle_days, oos_days)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.StrategyID, r.Fold, r.ISStart, r.ISEnd, r.OOSStart, r.OOSEnd,
			r.ISSharpe, r.OOSSharpe, r.ISReturnPct, r.OOSReturnPct,
			r.ISTrades, r.OOSTrades, r.ISMaxDD, r.OOSMaxDD, r.Trials,
			r.ISIdleDays, r.ISDays, r.OOSIdleDays, r.OOSDays); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return Summarize(db)
}

// Summarize rebuilds walk_forward_summary from every stored fold.
func Summarize(db *sql.DB) error {
	script, err := sqlfiles.Validation.ReadFile("validation/walk_forward_summary.sql")
	if err != nil {
		return err
	}
	_, err = db.Exec(string(script))
	return err
}

// FormatRows renders one strategy's folds for the CLI.
func FormatRows(rows []FoldRow) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-6s %-12s %-12s %8s %8s %8s %8s %6s %6s %9s\n",
		"fold", "oos_start", "oos_end", "is_shp", "oos_shp", "is_ret", "oos_ret", "is_n", "oos_n", "oos_idle")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-6d %-12s %-12s %8.2f %8.2f %7.1f%% %7.1f%% %6d %6d %4d/%-4d\n",
			r.Fold, r.OOSStart, r.OOSEnd, r.ISSharpe, r.OOSSharpe,
			r.ISReturnPct*100, r.OOSReturnPct*100, r.ISTrades, r.OOSTrades, r.OOSIdleDays, r.OOSDays)
	}
	return b.String()
}
