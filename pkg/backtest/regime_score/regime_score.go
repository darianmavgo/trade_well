// Package regime_score labels every trade in existing backtest results with the regime of a
// reference symbol (SPY by default) at the time the trade was initiated, then scores strategies
// by regime. The regime is the state of the last session strictly before the entry date, from
// the persisted Markov model (train markov). Nothing is re-run: it reads the trades tables in
// the numbered run folders under the reports directory.
package regime_score

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// DBName is the file Run writes in OutDir. It is created fresh by every run.
const DBName = "regime_score.db"

// Config holds every setting of a run.
type Config struct {
	RunsDir   string // folder of numbered run folders; empty = appenv.Reports()
	ModelDB   string // persisted Markov model; empty = appenv.MarkovDB()
	Symbol    string // regime symbol; empty = SPY
	OutDir    string // where regime_score.db is written; empty = RunsDir
	MinTrades int    // a cell under this many trades is flagged thin; 0 = 20
	Out       io.Writer
}

// Source is one results database whose trades were scored.
type Source struct {
	Path   string
	Report int
	Win    string // in_sample or oos
}

// Result reports what a run scored.
type Result struct {
	DB      string
	Sources []Source
	Skipped []string // result databases without a trades table
}

// resultFile matches <report>/<file>.db and <report>/oos/<file>.db under the runs folder.
var resultFile = regexp.MustCompile(`^(\d+)/(oos/)?[^/]+\.db$`)

// FindSources lists the results databases under runsDir, oldest report first.
func FindSources(runsDir string) ([]Source, error) {
	var out []Source
	err := filepath.WalkDir(runsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(runsDir, p)
		if err != nil {
			return err
		}
		m := resultFile.FindStringSubmatch(filepath.ToSlash(rel))
		if m == nil {
			return nil
		}
		n, _ := strconv.Atoi(m[1])
		win := "in_sample"
		if m[2] != "" {
			win = "oos"
		}
		out = append(out, Source{Path: p, Report: n, Win: win})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Report != out[j].Report {
			return out[i].Report < out[j].Report
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func hasTrades(path string) (bool, error) {
	db, err := storage.OpenSQLiteReadOnly(path)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'trades'`); err != nil {
		return false, err
	}
	return n > 0, nil
}

// uri is a read-only SQLite URI for path, so ATTACH can never write to a source database.
func uri(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return "file:" + strings.ReplaceAll(abs, "'", "''") + "?mode=ro", nil
}

// Run labels and scores the trades under cfg.RunsDir and writes OutDir/regime_score.db.
func Run(ctx context.Context, cfg Config) (Result, error) {
	var res Result
	if cfg.RunsDir == "" {
		cfg.RunsDir = appenv.Reports()
	}
	if cfg.ModelDB == "" {
		cfg.ModelDB = appenv.MarkovDB()
	}
	if cfg.Symbol == "" {
		cfg.Symbol = "SPY"
	}
	if cfg.OutDir == "" {
		cfg.OutDir = cfg.RunsDir
	}
	if cfg.MinTrades <= 0 {
		cfg.MinTrades = 20
	}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	if _, err := os.Stat(cfg.ModelDB); err != nil {
		return res, fmt.Errorf("regime_score: Markov model %s: %w (run train markov %s)", cfg.ModelDB, err, cfg.Symbol)
	}
	srcs, err := FindSources(cfg.RunsDir)
	if err != nil {
		return res, fmt.Errorf("regime_score: %w", err)
	}
	if len(srcs) == 0 {
		return res, fmt.Errorf("regime_score: no results databases under %s", cfg.RunsDir)
	}

	// A fresh database every run: no table from an earlier run can be read as this one's.
	if err := os.MkdirAll(cfg.OutDir, 0o755); err != nil {
		return res, err
	}
	res.DB = filepath.Join(cfg.OutDir, DBName)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(res.DB + suffix); err != nil && !os.IsNotExist(err) {
			return res, err
		}
	}
	db, err := storage.OpenSQLite(res.DB)
	if err != nil {
		return res, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // ATTACH is per connection
	if err := storage.RunStage(db, "regime_score_schema", nil); err != nil {
		return res, err
	}

	for _, s := range srcs {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		ok, err := hasTrades(s.Path)
		if err != nil {
			return res, fmt.Errorf("regime_score: %s: %w", s.Path, err)
		}
		if !ok {
			res.Skipped = append(res.Skipped, s.Path)
			continue
		}
		u, err := uri(s.Path)
		if err != nil {
			return res, err
		}
		rel, _ := filepath.Rel(cfg.RunsDir, s.Path)
		repl := map[string]string{
			"__SRC_URI__": u,
			"__SRC__":     strings.ReplaceAll(filepath.ToSlash(rel), "'", "''"),
			"__WIN__":     s.Win,
			"__REPORT__":  strconv.Itoa(s.Report),
		}
		if err := storage.RunStage(db, "regime_score_load", repl); err != nil {
			return res, err
		}
		res.Sources = append(res.Sources, s)
	}

	mu, err := uri(cfg.ModelDB)
	if err != nil {
		return res, err
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("ATTACH DATABASE '%s' AS model", mu)); err != nil {
		return res, fmt.Errorf("regime_score: attach model %s: %w", cfg.ModelDB, err)
	}
	var sessions int
	if err := db.GetContext(ctx, &sessions, `SELECT COUNT(*) FROM model.markov_prediction WHERE symbol = ?`, cfg.Symbol); err != nil {
		return res, fmt.Errorf("regime_score: read model: %w", err)
	}
	if sessions == 0 {
		return res, fmt.Errorf("regime_score: %s has no %s predictions (run train markov %s)", cfg.ModelDB, cfg.Symbol, cfg.Symbol)
	}
	repl := map[string]string{
		"__SYMBOL__":     strings.ReplaceAll(cfg.Symbol, "'", "''"),
		"__MIN_TRADES__": strconv.Itoa(cfg.MinTrades),
	}
	if err := storage.RunStage(db, "regime_score", repl); err != nil {
		return res, err
	}
	if _, err := db.ExecContext(ctx, "DETACH DATABASE model"); err != nil {
		return res, err
	}
	return res, report(ctx, cfg.Out, db, cfg)
}

// Cell is one scored row, for strategy cells and pooled cells alike.
type Cell struct {
	StrategyID string   `db:"strategy_id"`
	Win        string   `db:"win"`
	Regime     string   `db:"regime"`
	Strategies int      `db:"strategies"`
	N          int      `db:"n"`
	WinRate    float64  `db:"win_rate"`
	AvgReturn  float64  `db:"avg_return_pct"`
	TotalPnL   float64  `db:"total_net_pnl"`
	PF         *float64 `db:"profit_factor"`
	AvgHold    float64  `db:"avg_hold_days"`
	Thin       bool     `db:"thin"`
	VsAll      float64  `db:"vs_all_avg_return_pct"`
}

func report(ctx context.Context, w io.Writer, db *sqlx.DB, cfg Config) error {
	var cov []struct {
		What string `db:"what"`
		N    int    `db:"n"`
	}
	if err := db.SelectContext(ctx, &cov, `SELECT what, n FROM regime_coverage`); err != nil {
		return err
	}
	fmt.Fprintf(w, "[regime_score] regime = state of %s's last session before the entry date (model %s)\n", cfg.Symbol, cfg.ModelDB)
	for _, c := range cov {
		fmt.Fprintf(w, "[regime_score] %-32s %d\n", c.What, c.N)
	}
	order := `ORDER BY win, CASE regime WHEN 'bull' THEN 1 WHEN 'sideways' THEN 2 WHEN 'bear' THEN 3 WHEN 'unlabeled' THEN 4 ELSE 5 END`

	var pooled []Cell
	if err := db.SelectContext(ctx, &pooled, `SELECT '' AS strategy_id, win, regime, strategies, n, win_rate, avg_return_pct,
		total_net_pnl, profit_factor, avg_hold_days, thin, 0.0 AS vs_all_avg_return_pct FROM regime_pooled `+order); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nAll strategies pooled (thin = under %d trades)\n", cfg.MinTrades)
	printCells(w, pooled, false)

	var cells []Cell
	if err := db.SelectContext(ctx, &cells, `SELECT strategy_id, win, regime, 0 AS strategies, n, win_rate, avg_return_pct,
		total_net_pnl, profit_factor, avg_hold_days, thin, vs_all_avg_return_pct FROM regime_score
		ORDER BY strategy_id, win, CASE regime WHEN 'bull' THEN 1 WHEN 'sideways' THEN 2 WHEN 'bear' THEN 3 WHEN 'unlabeled' THEN 4 ELSE 5 END`); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nBy strategy (vs ALL = mean return minus the strategy's mean return over every regime)\n")
	printCells(w, cells, true)
	fmt.Fprintf(w, "\nSaved to %s\n", filepath.Join(cfg.OutDir, DBName))
	return nil
}

func printCells(w io.Writer, cells []Cell, byStrategy bool) {
	head := "%-10s %-10s %6s %7s %9s %12s %7s %8s %s\n"
	if byStrategy {
		fmt.Fprintf(w, "%-32s ", "strategy")
	}
	fmt.Fprintf(w, head, "window", "regime", "trades", "win%", "avg ret%", "net pnl", "PF", "avg hold", "")
	for _, c := range cells {
		if byStrategy {
			fmt.Fprintf(w, "%-32s ", c.StrategyID)
		}
		pf := "-"
		if c.PF != nil {
			pf = fmt.Sprintf("%.2f", *c.PF)
		}
		note := ""
		if c.Thin {
			note = "thin"
		}
		if byStrategy && c.Regime != "ALL" {
			note = strings.TrimSpace(fmt.Sprintf("%+.2f vs ALL %s", c.VsAll*100, note))
		}
		fmt.Fprintf(w, head, c.Win, c.Regime, strconv.Itoa(c.N), fmt.Sprintf("%.0f", c.WinRate*100),
			fmt.Sprintf("%.2f", c.AvgReturn*100), fmt.Sprintf("%.0f", c.TotalPnL), pf, fmt.Sprintf("%.1f", c.AvgHold), note)
	}
}
