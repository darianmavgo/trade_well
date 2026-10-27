// Package pipeline runs the whole path from raw bars to a ranked, validated set
// of strategies as one controlled run: market data, training, backtests, the
// parameter sweep, validation, stacks, and the comparisons.
//
// One run is one numbered folder under data/reports (the run id comes from
// storage.NewRun, which makes the folder atomically, so two runs cannot share
// an id). The pipeline keeps its own state in pipeline.db in that folder: the
// symbols and strategies the run is allowed to touch, and the status of each
// step. A run that stops can be resumed with its run id and skips the steps that
// finished. A lock file keeps two processes out of the same run folder.
//
// Every step is one of the existing commands called as a Go function with the
// run's scope, never a separate process.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/stratreg"
	"github.com/jmoiron/sqlx"
)

// Config is what a pipeline run is told. Everything else comes from the scope.
type Config struct {
	// Symbols are the stocks the run may touch. Empty means GOOGL on a new run
	// and the stored symbols on a resumed one.
	Symbols []string
	// Strategies, when set, are the only strategies the run may use. When empty
	// the run uses every strategy row tied to Symbols and picks up the rows its
	// own promote step adds.
	Strategies []string
	// Family, when set, scopes the run to the rows of one strategy family
	// ("streak") instead of the rows tied to Symbols. The symbols are the ones
	// those rows read and trade, the list is fixed like Strategies, and no
	// primary is chosen unless Primary is set, so the stack steps skip.
	Family string
	// Limit caps a Family run to its first N rows by id. 0 means every row.
	Limit   int
	Park    string // symbol idle cash parks in for the stack_eval step, default SGOV
	Primary string // primary of the stack steps, default streak-<sym>-down3-<sym>
	Years   int    // years of bars to download for what is missing, default 6
	// RunID 0 starts a new run. N resumes run N.
	RunID int
	// Steps limits the run to these steps (see StepNames). Skip removes steps.
	Steps    []string
	Skip     []string
	explicit map[string]bool // steps named in Steps, which run even when their scope says there is nothing to do
	// Redo runs steps again that already finished in a resumed run.
	Redo bool
	// SkipNetwork leaves out the steps that download data.
	SkipNetwork bool
	// Refresh downloads the whole window again for the symbols the run needs,
	// instead of only the bars missing from the market database.
	Refresh bool
	// PolygonKey enables the option-bar download and the covered-call step.
	PolygonKey string
	// MarketDB and RefDB default to the standard files.
	MarketDB string
	RefDB    string
	// Out receives the pipeline's own progress lines. Nil discards them.
	Out io.Writer
}

// StepResult is how one step ended.
type StepResult struct {
	Name   string
	Status string
	Error  string
}

// Result is what a run did.
type Result struct {
	RunID      int
	Dir        string
	Symbols    []string
	Strategies []string
	Steps      []StepResult
}

// Failed lists the steps that failed.
func (r Result) Failed() []StepResult {
	var out []StepResult
	for _, s := range r.Steps {
		if s.Status == StatusFailed {
			out = append(out, s)
		}
	}
	return out
}

// runCtx is what a step sees.
type runCtx struct {
	ctx        context.Context
	cfg        Config
	runID      int
	dir        string
	state      *sqlx.DB
	symbols    []string
	strategies []string
	primary    string
	out        io.Writer
	selected   map[string]bool // steps this run will try, by name
}

func (r *runCtx) ids() string { return strings.Join(r.strategies, ",") }

func (r *runCtx) printf(format string, args ...any) {
	if r.out != nil {
		fmt.Fprintf(r.out, format, args...)
	}
}

// Run controls one pipeline run. A step that fails is recorded and the run goes
// on with the next one. The returned error is for problems that stop the run
// before any step: bad scope, a locked run folder, an unreadable database.
func Run(ctx context.Context, cfg Config) (Result, error) {
	cfg = withDefaults(cfg)
	cfg.explicit = map[string]bool{}
	for _, n := range cfg.Steps {
		cfg.explicit[n] = true
	}
	if cfg.Out == nil {
		cfg.Out = io.Discard
	}
	root := appenv.Reports()

	runID, dir, resumed, err := openRun(root, cfg.RunID)
	if err != nil {
		return Result{}, err
	}
	lk, err := acquireLock(dir)
	if err != nil {
		return Result{}, err
	}
	defer lk.release()
	state, err := openState(dir)
	if err != nil {
		return Result{}, err
	}
	defer state.Close()

	stratreg.RegisterAll(appenv.Folder(), cfg.MarketDB)

	rc := &runCtx{ctx: ctx, cfg: cfg, runID: runID, dir: dir, state: state, out: cfg.Out}
	if err := rc.scope(resumed); err != nil {
		return Result{RunID: runID, Dir: dir}, err
	}
	rc.printf("📁 Run %d: %s\n", runID, dir)
	rc.printf("symbols: %s\n", strings.Join(rc.symbols, ","))
	rc.printf("%d strategies: %s\n", len(rc.strategies), rc.ids())

	res := Result{RunID: runID, Dir: dir, Symbols: rc.symbols, Strategies: rc.strategies}
	selected, err := selectSteps(cfg)
	if err != nil {
		return res, err
	}
	rc.selected = map[string]bool{}
	for _, st := range selected {
		rc.selected[st.name] = true
	}
	for i, st := range selected {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.Steps = append(res.Steps, rc.runStep(i+1, st))
	}
	res.Strategies = rc.strategies
	if err := finishRun(state); err != nil {
		return res, err
	}
	return res, nil
}

func withDefaults(cfg Config) Config {
	if cfg.Park == "" {
		cfg.Park = "SGOV"
	}
	if cfg.Years == 0 {
		cfg.Years = 6
	}
	if cfg.MarketDB == "" {
		cfg.MarketDB = appenv.MarketDB()
	}
	if cfg.RefDB == "" {
		cfg.RefDB = appenv.RefDB()
	}
	cfg.Park = strings.ToUpper(strings.TrimSpace(cfg.Park))
	return cfg
}

// openRun makes a new run folder (runID 0) or opens an existing one. The new
// folder is made by storage.NewRun, so the id is never reused.
func openRun(root string, runID int) (id int, dir string, resumed bool, err error) {
	if runID > 0 {
		dir, err = storage.RunDir(root, runID)
		if err != nil {
			return 0, "", false, err
		}
		if abs, aerr := filepath.Abs(dir); aerr == nil {
			dir = abs
		}
		return runID, dir, true, nil
	}
	id, dir, err = storage.NewRun(root)
	if err != nil {
		return 0, "", false, err
	}
	if abs, aerr := filepath.Abs(dir); aerr == nil {
		dir = abs
	}
	return id, dir, false, nil
}

// scope fixes what the run may touch. A new run takes it from the config. A
// resumed run reads it back, and a config that names different symbols is
// refused, so a run folder never mixes two scopes.
func (r *runCtx) scope(resumed bool) error {
	stored, hasRun, err := loadRun(r.state)
	if err != nil {
		return err
	}
	if resumed && !hasRun {
		return fmt.Errorf("pipeline: run %d has no pipeline state (it was not made by the pipeline)", r.runID)
	}
	if hasRun {
		syms, err := loadScope(r.state, "symbol")
		if err != nil {
			return err
		}
		if len(r.cfg.Symbols) > 0 {
			want, err := cleanSymbols(r.cfg.Symbols)
			if err != nil {
				return err
			}
			if strings.Join(want, ",") != strings.Join(syms, ",") {
				return fmt.Errorf("pipeline: run %d is scoped to %s, not %s", r.runID, strings.Join(syms, ","), strings.Join(want, ","))
			}
		}
		strategies, err := loadScope(r.state, "strategy")
		if err != nil {
			return err
		}
		r.symbols, r.strategies, r.primary = syms, strategies, stored.PrimaryID
		r.cfg.Park = stored.Park
		return nil
	}

	symbols := r.cfg.Symbols
	var strategies []string
	explicit := 0
	if r.cfg.Family != "" {
		if len(r.cfg.Strategies) > 0 || len(symbols) > 0 {
			return fmt.Errorf("pipeline: -family takes its symbols and strategies from the family; drop -symbol and -strategy")
		}
		explicit = 1
		strategies, symbols, err = strategiesForFamily(r.cfg.RefDB, r.cfg.Family, r.cfg.Limit)
		if err != nil {
			return err
		}
	} else {
		if len(symbols) == 0 {
			symbols = []string{"GOOGL"}
		}
		if symbols, err = cleanSymbols(symbols); err != nil {
			return err
		}
	}
	switch {
	case r.cfg.Family != "": // set above
	case len(r.cfg.Strategies) > 0:
		explicit = 1
		strategies = dedupe(r.cfg.Strategies)
		if err := checkRegistered(strategies); err != nil {
			return err
		}
	default:
		if strategies, err = strategiesFor(r.cfg.RefDB, symbols); err != nil {
			return err
		}
	}
	if len(strategies) == 0 {
		return fmt.Errorf("pipeline: no strategies for %s", strings.Join(symbols, ","))
	}
	r.symbols, r.strategies = symbols, strategies
	r.primary = r.cfg.Primary
	if r.primary == "" && r.cfg.Family == "" {
		r.primary = defaultPrimary(symbols, strategies)
	}
	if err := saveRun(r.state, runRow{RunID: r.runID, Park: r.cfg.Park, Bench: "", PrimaryID: r.primary, Explicit: explicit}); err != nil {
		return err
	}
	if _, err := addScope(r.state, "symbol", symbols); err != nil {
		return err
	}
	_, err = addScope(r.state, "strategy", strategies)
	return err
}

// defaultPrimary prefers streak-<sym>-down3-<sym> for the first symbol, then any
// streak strategy in scope.
func defaultPrimary(symbols, strategies []string) string {
	have := map[string]bool{}
	for _, s := range strategies {
		have[s] = true
	}
	for _, sym := range symbols {
		l := strings.ToLower(sym)
		if id := fmt.Sprintf("streak-%s-down3-%s", l, l); have[id] {
			return id
		}
	}
	for _, s := range strategies {
		if strings.HasPrefix(s, "streak-") {
			return s
		}
	}
	return ""
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// refreshScope adds the strategies tied to the run's symbols that did not exist
// when it started, such as rows its promote step wrote. A run with an explicit
// strategy list never grows.
func (r *runCtx) refreshScope() error {
	row, _, err := loadRun(r.state)
	if err != nil {
		return err
	}
	if row.Explicit == 1 {
		return nil
	}
	all, err := strategiesFor(r.cfg.RefDB, r.symbols)
	if err != nil {
		return err
	}
	added, err := addScope(r.state, "strategy", all)
	if err != nil {
		return err
	}
	if added > 0 {
		r.printf("scope grew by %d strategies\n", added)
	}
	r.strategies, err = loadScope(r.state, "strategy")
	return err
}

// runStep records, runs and times one step. A panic in a step is a failed step.
func (r *runCtx) runStep(seq int, st step) StepResult {
	res := StepResult{Name: st.name}
	prev, err := stepStatus(r.state, st.name)
	if err != nil {
		res.Status, res.Error = StatusFailed, err.Error()
		return res
	}
	if prev == StatusDone && !r.cfg.Redo {
		r.printf("\n== %s: already done in this run\n", st.name)
		res.Status = StatusDone
		return res
	}
	if why := st.skipReason(r); why != "" {
		r.printf("\n== %s: skipped, %s\n", st.name, why)
		_ = markStep(r.state, seq, st.name, StatusSkipped, why)
		res.Status, res.Error = StatusSkipped, why
		return res
	}
	r.printf("\n\033[1m== %s\033[0m\n", st.name)
	_ = markStep(r.state, seq, st.name, StatusRunning, "")
	err = runGuarded(func() error { return st.run(r) })
	storage.CloseSharedResults()
	if err != nil {
		r.printf("   !! FAILED: %s: %v\n", st.name, err)
		_ = markStep(r.state, seq, st.name, StatusFailed, err.Error())
		res.Status, res.Error = StatusFailed, err.Error()
		return res
	}
	_ = markStep(r.state, seq, st.name, StatusDone, "")
	res.Status = StatusDone
	return res
}

func runGuarded(fn func() error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return fn()
}

// join combines the errors of the parts of a step so one failing part does not
// hide the others.
func join(errs ...error) error { return errors.Join(errs...) }
