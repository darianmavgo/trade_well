package prune_losers

// `prune_losers symbols`: take the names that lose out of a rotation strategy's
// list. A name is a loser when it lost money in the in-sample backtest, lost money
// in the held-out backtest, and won fewer than -min-win-rate of its trades over both.
// All three must hold, so a name that only had a bad stretch in one window stays.
// The strategy is pointed at a new symbol list without the losers; the list it used
// is left as it was, because other strategies may use it.

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
)

// SymbolsConfig holds the settings of `prune_losers symbols`.
type SymbolsConfig struct {
	StrategiesDB string  // -db
	ReportsDir   string  // -reports
	Strategy     string  // -strategy: the rotation strategy id
	RunID        int     // -run-id: run folder with the in-sample and held-out results (0 = latest that has both)
	MinWinRate   float64 // -min-win-rate: a loser's win rate over both windows is below this
	DryRun       bool    // -dry-run
	Out          io.Writer
}

// SymbolStats is one name's trades in the two backtest windows.
type SymbolStats struct {
	Symbol         string
	ISTrades       int
	ISWins         int
	ISPnL          float64
	OOSTrades      int
	OOSWins        int
	OOSPnL         float64
	WinRate        float64 // wins over both windows, of both windows' trades
	Loser          bool
	ListedInSample bool // the name was in the strategy's list
}

// SymbolsResult is what was (or would be) changed.
type SymbolsResult struct {
	RunID     int
	Stats     []SymbolStats // every listed name that traded, worst total first
	Removed   []string
	Kept      []string
	OldList   string // symbol_lists id or "" for a literal list
	NewList   string // symbol_lists id the strategy now uses (the literal list stays literal)
	RunFolder string
}

func runSymbols(args []string, stdout, stderr io.Writer) int {
	cfg := SymbolsConfig{Out: stdout}
	fs := flag.NewFlagSet("prune_losers symbols", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.StrategiesDB, "db", appenv.RefDB(), "strategies database holding the rotation strategy and its symbol lists")
	fs.StringVar(&cfg.ReportsDir, "reports", appenv.Reports(), "reports folder with the numbered run folders")
	fs.StringVar(&cfg.Strategy, "strategy", "", "rotation strategy id to prune")
	fs.IntVar(&cfg.RunID, "run-id", 0, "run folder holding the strategy's in-sample (rotation.db) and held-out (oos/rotation.db) backtests (default: the latest with both)")
	fs.Float64Var(&cfg.MinWinRate, "min-win-rate", 0.4, "a name is a loser only if it won fewer than this share of its trades over both windows")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "report what would change without changing it")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if _, err := PruneSymbols(cfg); err != nil {
		fmt.Fprintln(stderr, "prune_losers symbols:", err)
		return 1
	}
	return 0
}

// PruneSymbols finds the losing names of cfg.Strategy and, unless DryRun, points
// the strategy at a symbol list without them.
func PruneSymbols(cfg SymbolsConfig) (SymbolsResult, error) {
	var res SymbolsResult
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	if strings.TrimSpace(cfg.Strategy) == "" {
		return res, fmt.Errorf("-strategy is required: the rotation strategy to prune")
	}
	db, err := refdb.Open(cfg.StrategiesDB)
	if err != nil {
		return res, err
	}
	defer db.Close()

	var raw string
	if err := db.Get(&raw, `SELECT symbols FROM rotation_strategy WHERE id = ?`, cfg.Strategy); err != nil {
		return res, fmt.Errorf("%q is not a rotation strategy in %s: %w", cfg.Strategy, cfg.StrategiesDB, err)
	}
	listed, isList, err := listedSymbols(db, raw)
	if err != nil {
		return res, err
	}
	if isList {
		res.OldList = strings.TrimSpace(raw)
	}
	if len(listed) == 0 {
		return res, fmt.Errorf("%s has no symbol list to prune", cfg.Strategy)
	}

	res.RunID, res.RunFolder, err = findRun(cfg.ReportsDir, cfg.RunID, cfg.Strategy)
	if err != nil {
		return res, err
	}
	is, err := tradesBySymbol(filepath.Join(res.RunFolder, "rotation.db"), cfg.Strategy)
	if err != nil {
		return res, err
	}
	oos, err := tradesBySymbol(filepath.Join(res.RunFolder, "oos", "rotation.db"), cfg.Strategy)
	if err != nil {
		return res, err
	}

	inList := map[string]bool{}
	for _, s := range listed {
		inList[s] = true
	}
	for _, sym := range listed {
		a, b := is[sym], oos[sym]
		if a.trades+b.trades == 0 {
			res.Kept = append(res.Kept, sym)
			continue
		}
		st := SymbolStats{Symbol: sym, ISTrades: a.trades, ISWins: a.wins, ISPnL: a.pnl, OOSTrades: b.trades, OOSWins: b.wins, OOSPnL: b.pnl, ListedInSample: true}
		st.WinRate = float64(a.wins+b.wins) / float64(a.trades+b.trades)
		// A name with no trades in a window has no loss there to count.
		st.Loser = a.trades > 0 && b.trades > 0 && a.pnl < 0 && b.pnl < 0 && st.WinRate < cfg.MinWinRate
		res.Stats = append(res.Stats, st)
		if st.Loser {
			res.Removed = append(res.Removed, sym)
		} else {
			res.Kept = append(res.Kept, sym)
		}
	}
	sort.Slice(res.Stats, func(i, j int) bool {
		return res.Stats[i].ISPnL+res.Stats[i].OOSPnL < res.Stats[j].ISPnL+res.Stats[j].OOSPnL
	})

	fmt.Fprintf(cfg.Out, "run %d (%s): %s, %d names traded, loser = loses in-sample AND held-out AND win rate < %.0f%%\n",
		res.RunID, res.RunFolder, cfg.Strategy, len(res.Stats), cfg.MinWinRate*100)
	fmt.Fprintf(cfg.Out, "%-8s %6s %10s %6s %10s %8s  %s\n", "symbol", "is_n", "is_pnl", "oos_n", "oos_pnl", "win", "")
	for _, st := range res.Stats {
		if st.ISPnL+st.OOSPnL >= 0 && !st.Loser {
			continue // the table is the losing end of the list
		}
		mark := ""
		if st.Loser {
			mark = "REMOVE"
		}
		fmt.Fprintf(cfg.Out, "%-8s %6d %10.0f %6d %10.0f %7.0f%%  %s\n", st.Symbol, st.ISTrades, st.ISPnL, st.OOSTrades, st.OOSPnL, st.WinRate*100, mark)
	}
	if len(res.Removed) == 0 {
		fmt.Fprintln(cfg.Out, "no name meets all three conditions; nothing to remove")
		return res, nil
	}
	if res.OldList != "" {
		res.NewList = strings.TrimSuffix(res.OldList, "-pruned") + "-pruned"
	}
	if cfg.DryRun {
		fmt.Fprintf(cfg.Out, "dry run: would remove %s (%d of %d names), %d would remain\n", strings.Join(res.Removed, ", "), len(res.Removed), len(listed), len(res.Kept))
		return res, nil
	}

	kept := keptSorted(listed, res.Removed)
	tx, err := db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	newValue := strings.Join(kept, ",")
	if res.NewList != "" {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO symbol_lists (symbol_list_id, list) VALUES (?, ?)`, res.NewList, newValue); err != nil {
			return res, err
		}
		newValue = res.NewList
	}
	if _, err := tx.Exec(`UPDATE rotation_strategy SET symbols = ? WHERE id = ?`, newValue, cfg.Strategy); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	where := "its literal symbol list"
	if res.NewList != "" {
		where = fmt.Sprintf("symbol list %s (a copy of %s without them; %s is unchanged)", res.NewList, res.OldList, res.OldList)
	}
	fmt.Fprintf(cfg.Out, "removed %s from %s: now %d names in %s\n", strings.Join(res.Removed, ", "), cfg.Strategy, len(kept), where)
	return res, nil
}

// listedSymbols is the names of a row's symbols value: a symbol_lists id or a
// comma-separated literal list. isList says which.
func listedSymbols(db interface {
	Get(dest any, query string, args ...any) error
}, raw string) (names []string, isList bool, err error) {
	raw = strings.TrimSpace(raw)
	var list string
	if e := db.Get(&list, `SELECT list FROM symbol_lists WHERE symbol_list_id = ?`, raw); e == nil {
		isList = true
		raw = list
	} else if e != sql.ErrNoRows {
		return nil, false, e
	}
	seen := map[string]bool{}
	for _, s := range strings.Split(raw, ",") {
		if s = strings.ToUpper(strings.TrimSpace(s)); s != "" && !seen[s] {
			seen[s] = true
			names = append(names, s)
		}
	}
	return names, isList, nil
}

func keptSorted(listed, removed []string) []string {
	drop := map[string]bool{}
	for _, r := range removed {
		drop[r] = true
	}
	var out []string
	for _, s := range listed {
		if !drop[s] {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

type symbolTrades struct {
	trades, wins int
	pnl          float64
}

// tradesBySymbol sums the strategy's latest run in a result database by symbol.
// A missing file or a strategy with no run there has no trades.
func tradesBySymbol(path, strategyID string) (map[string]symbolTrades, error) {
	out := map[string]symbolTrades{}
	if _, err := os.Stat(path); err != nil {
		return out, nil
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('runs','trades')`).Scan(&n); err != nil || n < 2 {
		return out, err
	}
	var runID sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(run_id) FROM runs WHERE strategy_id = ?`, strategyID).Scan(&runID); err != nil || !runID.Valid {
		return out, err
	}
	rows, err := db.Query(`SELECT symbol, COUNT(*), SUM(CASE WHEN net_pnl > 0 THEN 1 ELSE 0 END), COALESCE(SUM(net_pnl), 0)
		FROM trades WHERE run_id = ? AND strategy_id = ? GROUP BY symbol`, runID.Int64, strategyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sym string
		var t symbolTrades
		if err := rows.Scan(&sym, &t.trades, &t.wins, &t.pnl); err != nil {
			return nil, err
		}
		out[strings.ToUpper(sym)] = t
	}
	return out, rows.Err()
}

// findRun returns the run folder to read: runID when given, else the highest
// numbered folder whose rotation.db and oos/rotation.db both hold a run of the strategy.
func findRun(reports string, runID int, strategyID string) (int, string, error) {
	has := func(path string) bool {
		m, err := tradesBySymbol(path, strategyID)
		return err == nil && len(m) > 0
	}
	if runID > 0 {
		dir := filepath.Join(reports, strconv.Itoa(runID))
		if _, err := os.Stat(dir); err != nil {
			return 0, "", fmt.Errorf("no run %d in %s", runID, reports)
		}
		if !has(filepath.Join(dir, "rotation.db")) || !has(filepath.Join(dir, "oos", "rotation.db")) {
			return 0, "", fmt.Errorf("run %d has no in-sample and held-out trades of %s (rotation.db and oos/rotation.db)", runID, strategyID)
		}
		return runID, dir, nil
	}
	entries, err := os.ReadDir(reports)
	if err != nil {
		return 0, "", err
	}
	var runs []int
	for _, e := range entries {
		if n, err := strconv.Atoi(e.Name()); err == nil && e.IsDir() && n > 0 {
			runs = append(runs, n)
		}
	}
	sort.Sort(sort.Reverse(sort.IntSlice(runs)))
	for _, n := range runs {
		dir := filepath.Join(reports, strconv.Itoa(n))
		if has(filepath.Join(dir, "rotation.db")) && has(filepath.Join(dir, "oos", "rotation.db")) {
			return n, dir, nil
		}
	}
	return 0, "", fmt.Errorf("no run in %s has both an in-sample and a held-out backtest of %s: run backtest -strategy %s first", reports, strategyID, strategyID)
}
