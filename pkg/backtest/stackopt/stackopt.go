// Package stackopt builds a stack by greedy forward selection on one shared
// cash ledger: the first sleeve is the best single strategy that stays inside
// the drawdown cap, and each round adds the candidate that raises the STACK's
// CAGR most while the STACK's max drawdown stays inside the cap. Selection sees
// only the in-sample window; the held-out months are run once at the end.
package stackopt

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// Config holds every setting of one optimizer run.
type Config struct {
	DB            string
	Table         string
	Candidates    []string // strategy ids; at least one
	Start         string   // earliest simulated date; empty = full history
	HoldoutMonths int      // 0 = no holdout, select on all history
	Capital       float64
	Alloc         float64 // per-position fraction of equity; 0 keeps each strategy's own
	MaxDD         float64 // stack max drawdown cap, fraction (0.10 = 10%)
	MinGain       float64 // stop when the best addition lifts stack CAGR by less than this
	MaxSleeves    int
	Concurrency   int
	OutDir        string    // reports root; a new numbered run folder is made inside
	AllowLeaks    bool      // keep tree strategies whose model was trained past the holdout cutoff (see `train check`)
	LeakReport    string    // training_leaks.db from `train check`; empty = appenv.ReportFile("training_leaks.db")
	Name          string    // when set, the result is saved to the refdata stack table under this name
	Out           io.Writer // progress; nil discards
}

// Step is the stack after one selection round.
type Step struct {
	Added     string
	CAGR      float64
	MaxDD     float64
	Trades    int
	FinalEq   float64
	Evaluated int
}

// Result is the optimizer's outcome.
type Result struct {
	StackID   string
	Steps     []Step
	InSample  models.PerformanceReport
	OOS       *models.PerformanceReport // nil when there is no holdout
	RunDir    string
	InEnd     string
	OOSStart  string
	Persisted bool
}

func (c *Config) norm() {
	if c.Table == "" {
		c.Table = runner.DefaultBarsTable
	}
	if c.Capital <= 0 {
		c.Capital = 100000
	}
	if c.MaxDD <= 0 {
		c.MaxDD = 0.10
	}
	if c.MinGain <= 0 {
		c.MinGain = 0.005
	}
	if c.MaxSleeves <= 0 {
		c.MaxSleeves = 12
	}
	if c.Concurrency <= 0 {
		c.Concurrency = 8
	}
	if c.OutDir == "" {
		c.OutDir = appenv.Reports()
	}
	if c.Out == nil {
		c.Out = io.Discard
	}
}

// candidate is one resolved strategy with its in-sample signals, untagged.
type candidate struct {
	strat   strategy.Strategy
	signals []models.Signal
}

func tag(sigs []models.Signal, id string, priority int) []models.Signal {
	out := make([]models.Signal, len(sigs))
	copy(out, sigs)
	for i := range out {
		out[i].StrategyID = id
		out[i].Priority = priority
	}
	return out
}

// Run selects the stack, runs the held-out months once, and writes both
// windows to a new run folder as stack.db and oos/stack.db.
func Run(cfg Config) (*Result, error) {
	cfg.norm()
	if cfg.DB == "" || len(cfg.Candidates) == 0 {
		return nil, fmt.Errorf("stackopt: DB and at least one candidate are required")
	}
	if !cfg.AllowLeaks {
		kept, dropped, err := dropLeaked(cfg)
		if err != nil {
			return nil, err
		}
		if dropped > 0 {
			fmt.Fprintf(cfg.Out, "stackopt: dropped %d tree candidates trained past the holdout cutoff (train check)\n", dropped)
		}
		cfg.Candidates = kept
	}
	var strats []strategy.Strategy
	for _, id := range cfg.Candidates {
		s, ok := strategy.Get(strings.TrimSpace(id))
		if !ok {
			return nil, fmt.Errorf("stackopt: strategy %q not found in registry", id)
		}
		if _, isPark := s.(strategy.ResidualProvider); isPark {
			return nil, fmt.Errorf("stackopt: %s is a park, not a sleeve", s.ID())
		}
		strats = append(strats, s)
	}

	inEnd, oosStart, split, err := holdout(cfg)
	if err != nil {
		return nil, err
	}
	db, err := storage.OpenSQLite(cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("stackopt: open %s: %w", cfg.DB, err)
	}
	defer db.Close()
	symbols := append(runner.RequiredSymbolsFor(strats, ""), "SPY")
	bars, dates, err := storage.FetchBars(db, cfg.Table, symbols, cfg.Start, inEnd)
	if err != nil {
		return nil, fmt.Errorf("stackopt: load bars: %w", err)
	}
	calc, err := os.MkdirTemp("", "stackopt_calc")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(calc)

	fmt.Fprintf(cfg.Out, "stackopt: %d candidates, selecting on %s, drawdown cap %.1f%%\n", len(strats), window(cfg.Start, inEnd), cfg.MaxDD*100)
	cands := generate(cfg, strats, bars, calc)

	steps, chosen := greedy(cfg, cands, bars, dates, calc)
	if len(chosen) == 0 {
		return nil, fmt.Errorf("stackopt: no candidate keeps the stack inside %.1f%% drawdown", cfg.MaxDD*100)
	}
	res := &Result{Steps: steps, InEnd: inEnd, OOSStart: oosStart}
	ids := make([]string, len(chosen))
	for i, c := range chosen {
		ids[i] = c.strat.ID()
	}
	res.StackID = strategy.StackID(ids...)

	_, runDir, err := storage.NewRun(cfg.OutDir)
	if err != nil {
		return nil, err
	}
	res.RunDir = runDir
	in := stack(cfg, chosen, bars, dates, calc, runDir, true)
	if in.Err != nil {
		return nil, in.Err
	}
	res.InSample = in.CombinedReport

	if split {
		oosDir := filepath.Join(runDir, "oos")
		if err := os.MkdirAll(oosDir, 0o755); err != nil {
			return nil, err
		}
		fullBars, fullDates, err := storage.FetchBars(db, cfg.Table, symbols, oosStart, "")
		if err != nil {
			return nil, fmt.Errorf("stackopt: load held-out bars: %w", err)
		}
		var strs []strategy.Strategy
		for _, c := range chosen {
			strs = append(strs, c.strat)
		}
		oosCands := generate(cfg, strs, fullBars, calc)
		oos := stack(cfg, oosCands, fullBars, fullDates, calc, oosDir, true)
		if oos.Err != nil {
			return nil, oos.Err
		}
		res.OOS = &oos.CombinedReport
	}
	if cfg.Name != "" {
		if err := saveName(cfg, res); err != nil {
			return nil, err
		}
		res.Persisted = true
	}
	return res, nil
}

func holdout(cfg Config) (inEnd, oosStart string, split bool, err error) {
	end, ok, err := storage.InSampleEnd(cfg.DB, cfg.Table, cfg.Start, "", cfg.HoldoutMonths)
	if err != nil || !ok {
		return "", "", false, err
	}
	return end, nextDay(end), true, nil
}

func window(start, end string) string {
	if start == "" {
		start = "start of history"
	}
	if end == "" {
		end = "latest bar"
	}
	return start + " to " + end
}

// generate produces each candidate's signals once, in parallel.
func generate(cfg Config, strats []strategy.Strategy, bars map[string][]models.Bar, calc string) []candidate {
	out := make([]candidate, len(strats))
	var wg sync.WaitGroup
	sem := make(chan struct{}, cfg.Concurrency)
	for i, s := range strats {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, s strategy.Strategy) {
			defer wg.Done()
			defer func() { <-sem }()
			s.SetDatabases(cfg.DB, filepath.Join(calc, "calc_"+sanitize(s.ID())+".db"))
			out[i] = candidate{strat: s, signals: s.GenerateSignals(bars)}
		}(i, s)
	}
	wg.Wait()
	return out
}

func sanitize(id string) string {
	return strings.NewReplacer("/", "_", "+", "_", " ", "_").Replace(id)
}

// stack runs members (first = primary) on one ledger. persist writes dir/stack.db.
func stack(cfg Config, members []candidate, bars map[string][]models.Bar, dates []string, calc, dir string, persist bool) runner.SharedRunResult {
	var all []models.Signal
	for i, m := range members {
		all = append(all, tag(m.signals, m.strat.ID(), i)...)
	}
	var secs []strategy.Strategy
	for _, m := range members[1:] {
		secs = append(secs, m.strat)
	}
	return runner.ExecuteStack(runner.StackRequest{
		Primary:      members[0].strat,
		Secondaries:  secs,
		BarsBySymbol: bars,
		SortedDates:  dates,
		Capital:      cfg.Capital,
		OutDir:       dir,
		MarketDBPath: cfg.DB,
		Persist:      persist,
		Signals:      all,
		CalcDir:      calc,
		Override:     runner.ConfigOverride{AllocPct: cfg.Alloc},
	})
}

type trial struct {
	cand int
	res  runner.SharedRunResult
}

// greedy runs the selection rounds. It returns the steps and the chosen
// candidates in priority order.
func greedy(cfg Config, cands []candidate, bars map[string][]models.Bar, dates []string, calc string) ([]Step, []candidate) {
	var chosen []candidate
	var steps []Step
	used := map[int]bool{}
	bestCAGR := 0.0
	for len(chosen) < cfg.MaxSleeves {
		var todo []int
		for i := range cands {
			if !used[i] {
				todo = append(todo, i)
			}
		}
		results := make([]trial, len(todo))
		var wg sync.WaitGroup
		sem := make(chan struct{}, cfg.Concurrency)
		for k, i := range todo {
			wg.Add(1)
			sem <- struct{}{}
			go func(k, i int) {
				defer wg.Done()
				defer func() { <-sem }()
				members := append(append([]candidate{}, chosen...), cands[i])
				results[k] = trial{cand: i, res: stack(cfg, members, bars, dates, calc, "", false)}
			}(k, i)
		}
		wg.Wait()

		pick := -1
		for k, t := range results {
			r := t.res
			if r.Err != nil || r.CombinedReport.MaxDrawdownPct > cfg.MaxDD {
				continue
			}
			if pick < 0 || r.CombinedReport.CAGR > results[pick].res.CombinedReport.CAGR {
				pick = k
			}
		}
		if pick < 0 {
			break
		}
		r := results[pick].res.CombinedReport
		if len(chosen) > 0 && r.CAGR-bestCAGR < cfg.MinGain {
			break
		}
		bestCAGR = r.CAGR
		chosen = append(chosen, cands[results[pick].cand])
		used[results[pick].cand] = true
		steps = append(steps, Step{
			Added: cands[results[pick].cand].strat.ID(), CAGR: r.CAGR, MaxDD: r.MaxDrawdownPct,
			Trades: r.TotalTrades, FinalEq: r.FinalEquity, Evaluated: len(todo),
		})
		fmt.Fprintf(cfg.Out, "  round %2d: +%-40s stack CAGR %6.1f%%  DD %5.1f%%  trades %d\n",
			len(chosen), steps[len(steps)-1].Added, r.CAGR*100, r.MaxDrawdownPct*100, r.TotalTrades)
	}
	return steps, chosen
}

// saveName writes the stack to the refdata stack table.
func saveName(cfg Config, res *Result) error {
	db, err := refdb.Open(refdb.DefaultPath)
	if err != nil {
		return err
	}
	defer db.Close()
	return upsertStack(db, cfg.Name, res)
}

func upsertStack(db *sqlx.DB, name string, res *Result) error {
	note := fmt.Sprintf("stackopt: in-sample %.1f%% CAGR / %.1f%% DD", res.InSample.CAGR*100, res.InSample.MaxDrawdownPct*100)
	if res.OOS != nil {
		note += fmt.Sprintf("; held-out %.1f%% / %.1f%%", res.OOS.CAGR*100, res.OOS.MaxDrawdownPct*100)
	}
	note += "; run folder " + filepath.Base(res.RunDir)
	_, err := db.Exec(`INSERT OR REPLACE INTO stack (name, id, note) VALUES (?, ?, ?)`, name, res.StackID, note)
	return err
}

// Summary returns a printable verdict table.
func (r *Result) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "\nStack (%d sleeves): %s\nRun folder: %s\n", len(r.Steps), r.StackID, r.RunDir)
	fmt.Fprintf(&b, "  in-sample  %-24s CAGR %7.1f%%  max DD %5.1f%%  trades %d\n", "(to "+r.InEnd+")", r.InSample.CAGR*100, r.InSample.MaxDrawdownPct*100, r.InSample.TotalTrades)
	if r.OOS != nil {
		fmt.Fprintf(&b, "  held-out   %-24s CAGR %7.1f%%  max DD %5.1f%%  trades %d\n", "(from "+r.OOSStart+")", r.OOS.CAGR*100, r.OOS.MaxDrawdownPct*100, r.OOS.TotalTrades)
	}
	return b.String()
}

var _ = sort.Strings

func nextDay(d string) string {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return d
	}
	return t.AddDate(0, 0, 1).Format("2006-01-02")
}

// dropLeaked removes tree candidates whose signal symbol is listed in
// training_leak. A missing report is an error: run `train check` first.
func dropLeaked(cfg Config) ([]string, int, error) {
	path := cfg.LeakReport
	if path == "" {
		path = appenv.ReportFile("training_leaks.db")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, 0, fmt.Errorf("stackopt: %s not found: run `train check` first, or pass -allow-leaks", path)
	}
	ldb, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, 0, err
	}
	defer ldb.Close()
	var syms []string
	if err := ldb.Select(&syms, "SELECT symbol FROM training_leak WHERE family = 'tree'"); err != nil {
		return nil, 0, fmt.Errorf("stackopt: read %s: %w", path, err)
	}
	leaked := map[string]bool{}
	for _, s := range syms {
		leaked[strings.ToUpper(s)] = true
	}
	rdb, err := refdb.Open(refdb.DefaultPath)
	if err != nil {
		return nil, 0, err
	}
	defer rdb.Close()
	var kept []string
	dropped := 0
	for _, id := range cfg.Candidates {
		if row, ok, _ := refdb.TreeStrategyByID(rdb, strings.TrimSpace(id)); ok && leaked[strings.ToUpper(row.SignalSymbol)] {
			dropped++
			continue
		}
		kept = append(kept, id)
	}
	return kept, dropped, nil
}
