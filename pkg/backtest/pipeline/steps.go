package pipeline

import (
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/backtest"
	"github.com/darianmavgo/trade_well/pkg/backtest/check_overfit"
	"github.com/darianmavgo/trade_well/pkg/backtest/gridsearch"
	"github.com/darianmavgo/trade_well/pkg/backtest/market_history"
	"github.com/darianmavgo/trade_well/pkg/backtest/park_sweep"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/scoreboard"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/strateval"
	"github.com/darianmavgo/trade_well/pkg/backtest/study"
	"github.com/darianmavgo/trade_well/pkg/backtest/train"
	"github.com/darianmavgo/trade_well/pkg/backtest/validate"
	"github.com/darianmavgo/trade_well/pkg/backtest/walk_forward"
)

// step is one stage of the pipeline. Its name is the job name used everywhere
// else: the command, the log prefix and the pipeline_step row.
type step struct {
	name         string
	network      bool // downloads data
	needsKey     bool // needs an option-data key
	needsPrimary bool // needs a stack primary in scope
	// when returns why the step has nothing to do for this scope, or "" when it
	// does. A step the user names in -steps always runs, so when only decides
	// the default run.
	when func(*runCtx) string
	run  func(*runCtx) error
}

// skipReason says why a step cannot run in this run, or "" when it can.
func (s step) skipReason(r *runCtx) string {
	switch {
	case s.network && r.cfg.SkipNetwork:
		return "network steps are off"
	case s.needsKey && r.cfg.PolygonKey == "":
		return "no option-data key (POLYGON_API_KEY)"
	case s.needsPrimary && r.primary == "":
		return "no primary strategy in scope"
	}
	if s.when != nil && !r.cfg.explicit[s.name] {
		return s.when(r)
	}
	return ""
}

// allSteps is the pipeline in order.
func allSteps() []step {
	return []step{
		{name: "market_history", network: true, run: stepMarketHistory},
		{name: "market_history_options", network: true, needsKey: true, when: whenCoveredCall, run: stepMarketHistoryOptions},
		{name: "study", when: whenHMM, run: stepStudy},
		{name: "train", when: whenTrainable, run: stepTrain},
		{name: "backtest", run: stepBacktest},
		{name: "gridsearch_params", run: stepGridsearchParams},
		{name: "gridsearch", run: stepGridsearch},
		{name: "gridsearch_promote", needsPrimary: true, run: stepPromote},
		{name: "gridsearch_apply", run: stepApply},
		{name: "validate", run: stepValidate},
		{name: "strateval", run: stepStrateval},
		{name: "stack", needsPrimary: true, run: stepStack},
		{name: "stack_eval", needsPrimary: true, run: stepStackEval},
		{name: "covered_call", network: true, needsKey: true, when: whenAskedFor, run: stepCoveredCall},
		{name: "park_sweep", run: stepParkSweep},
		{name: "scoreboard", run: stepScoreboard},
		{name: "stale", run: stepStale},
	}
}

// StepNames lists the steps in order.
func StepNames() []string {
	var out []string
	for _, s := range allSteps() {
		out = append(out, s.name)
	}
	return out
}

// selectSteps applies Config.Steps and Config.Skip, refusing a name that is not a step.
func selectSteps(cfg Config) ([]step, error) {
	known := map[string]bool{}
	for _, n := range StepNames() {
		known[n] = true
	}
	for _, list := range [][]string{cfg.Steps, cfg.Skip} {
		for _, n := range list {
			if !known[n] {
				return nil, fmt.Errorf("pipeline: unknown step %q (steps: %s)", n, strings.Join(StepNames(), ", "))
			}
		}
	}
	only := map[string]bool{}
	for _, n := range cfg.Steps {
		only[n] = true
	}
	skip := map[string]bool{}
	for _, n := range cfg.Skip {
		skip[n] = true
	}
	var out []step
	for _, s := range allSteps() {
		if (len(only) > 0 && !only[s.name]) || skip[s.name] {
			continue
		}
		out = append(out, s)
	}
	return out, nil
}

// --- what the scope needs ---

// members returns the registered strategies in scope.
func (r *runCtx) members() []strategy.Strategy {
	var out []strategy.Strategy
	for _, id := range r.strategies {
		if s, ok := strategy.Get(id); ok {
			out = append(out, s)
		}
	}
	return out
}

// inFamily returns the strategies in scope that belong to the family.
func (r *runCtx) inFamily(family string) []strategy.Strategy {
	var out []strategy.Strategy
	for _, s := range r.members() {
		if strategy.FamilyOf(s) == family {
			out = append(out, s)
		}
	}
	return out
}

// neededSymbols is the bars this run reads: the run's symbols, every symbol its
// strategies declare, and each strategy's benchmark (runner.RequiredSymbolsFor),
// plus the park symbol when the stack_eval step is going to run.
func (r *runCtx) neededSymbols() []string {
	set := map[string]bool{}
	for _, s := range r.symbols {
		set[s] = true
	}
	for _, s := range runner.RequiredSymbolsFor(r.members(), "") {
		set[s] = true
	}
	if r.selected["stack_eval"] && r.primary != "" && r.cfg.Park != "" {
		set[r.cfg.Park] = true
	}
	var out []string
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// coveredCallUnderlyings are the symbols of the covered-call strategies in scope,
// the only strategies that read option history.
func (r *runCtx) coveredCallUnderlyings() []string {
	var out []string
	for _, id := range r.strategies {
		if i := strings.Index(id, "-covered-call"); i > 0 {
			out = append(out, strings.ToUpper(id[:i]))
		}
	}
	return dedupe(out)
}

func whenCoveredCall(r *runCtx) string {
	if len(r.coveredCallUnderlyings()) == 0 {
		return "no covered-call strategy in scope (name the step in -steps to run it anyway)"
	}
	return ""
}

func whenAskedFor(r *runCtx) string {
	return "not part of building these strategies (name the step in -steps to run it)"
}

func whenHMM(r *runCtx) string {
	for _, id := range r.strategies {
		if strings.HasPrefix(id, "markov_hmm") {
			return ""
		}
	}
	return "no markov_hmm strategy in scope"
}

func whenTrainable(r *runCtx) string {
	if len(r.inFamily("markov")) == 0 && len(r.inFamily("tree")) == 0 {
		return "no markov or tree strategy in scope"
	}
	return ""
}

// trainSymbols are the symbols the family's strategies in scope declare.
func (r *runCtx) trainSymbols(family string) []string {
	return runner.RequiredSymbolsFor(r.inFamily(family), "")
}

// --- data, studies, training ---

func stepMarketHistory(r *runCtx) error {
	syms := r.neededSymbols()
	r.printf("symbols the scope reads: %s\n", strings.Join(syms, ","))
	mc := market_history.DefaultConfig()
	mc.DB = r.cfg.MarketDB
	mc.Symbols = strings.Join(syms, ",")
	mc.Years = r.cfg.Years
	mc.Force = r.cfg.Refresh
	mc.Out = r.out
	_, err := market_history.Run(r.ctx, mc)
	return err
}

func stepMarketHistoryOptions(r *runCtx) error {
	syms := r.coveredCallUnderlyings()
	if len(syms) == 0 { // named in -steps without a covered-call strategy: the run's symbols
		syms = r.symbols
	}
	mc := market_history.DefaultConfig()
	mc.DB = r.cfg.MarketDB
	mc.Source = "polygon-options"
	mc.PolygonKey = r.cfg.PolygonKey
	mc.Symbols = strings.Join(syms, ",")
	mc.Out = r.out
	_, err := market_history.Run(r.ctx, mc)
	return err
}

func stepStudy(r *runCtx) error {
	var hmm []strategy.Strategy
	for _, s := range r.inFamily("markov") {
		if strings.HasPrefix(s.ID(), "markov_hmm") {
			hmm = append(hmm, s)
		}
	}
	var errs []error
	for _, sym := range runner.RequiredSymbolsFor(hmm, "") {
		sc := study.DefaultConfig()
		sc.DB = r.cfg.MarketDB
		sc.Study = "hmm_regime"
		sc.Symbol = sym
		if _, err := study.Run(sc); err != nil {
			errs = append(errs, fmt.Errorf("hmm_regime %s: %w", sym, err))
		}
	}
	return join(errs...)
}

func stepTrain(r *runCtx) error {
	var errs []error
	for _, family := range []string{"markov", "tree"} {
		syms := r.trainSymbols(family)
		if len(syms) == 0 {
			continue
		}
		args := append([]string{family}, syms...)
		if code := train.Run(args, r.out, r.out); code != 0 {
			errs = append(errs, fmt.Errorf("train %s exited %d", family, code))
		}
	}
	return join(errs...)
}

// --- backtest, sweep, tune ---

func stepBacktest(r *runCtx) error {
	bt := backtest.DefaultConfig()
	bt.Db = r.cfg.MarketDB
	bt.Strategy = r.ids()
	bt.RunID = r.runID
	bt.Force = true
	return backtest.Run(bt)
}

func gridConfig(r *runCtx) gridsearch.Config {
	gs := gridsearch.DefaultConfig()
	gs.Db = r.cfg.MarketDB
	gs.RunID = r.runID
	return gs
}

func stepGridsearchParams(r *runCtx) error {
	var errs []error
	for _, id := range r.strategies {
		gs := gridConfig(r)
		gs.Subcommand = "params"
		gs.Args = []string{id}
		if err := gridsearch.Run(gs); err != nil {
			errs = append(errs, fmt.Errorf("params %s: %w", id, err))
		}
	}
	return join(errs...)
}

func stepGridsearch(r *runCtx) error {
	gs := gridConfig(r) // End stays empty: gridsearch holds out the same months backtest does
	gs.Strategy = r.ids()
	gs.Force = true
	return gridsearch.Run(gs)
}

func stepPromote(r *runCtx) error {
	gs := gridConfig(r)
	gs.Subcommand = "promote"
	gs.Strategy = r.primary
	if err := gridsearch.Run(gs); err != nil {
		return err
	}
	return r.refreshScope()
}

func stepApply(r *runCtx) error {
	gs := gridConfig(r)
	gs.Subcommand = "apply"
	gs.Strategy = r.ids()
	return gridsearch.Run(gs)
}

// --- validate and score ---

func stepValidate(r *runCtx) error {
	return validate.Run(r.ctx, r.out, validate.Config{
		Walk: walk_forward.Config{
			Strategy: r.ids(), MarketDB: r.cfg.MarketDB, Table: "backtest_start", KeepGoing: true,
			Options: walk_forward.Options{TrainMonths: 24, TestMonths: 6, StepMonths: 6, Capital: 100000},
		},
		Check: check_overfit.Config{
			Strategy: r.ids(), Gates: check_overfit.Gates{MinOOSTrades: 8, TrialCutoff: 20, Decay: 0.25},
		},
		RunID: r.runID,
	})
}

func stepStrateval(r *runCtx) error {
	sc := strateval.DefaultConfig()
	sc.MarketDb = r.cfg.MarketDB
	sc.RunFolder = r.runID
	sc.Strategy = r.ids()
	sc.Optimize = true
	sc.MaxTrials = 50
	errs := []error{strateval.Run(sc)}
	for _, sub := range []string{"report", "status"} {
		rc := strateval.DefaultConfig()
		rc.RunFolder = r.runID
		rc.Strategy = r.ids()
		rc.Subcommand = sub
		errs = append(errs, strateval.Run(rc))
	}
	return join(errs...)
}

// --- stacks ---

// stackMembers is the primary, each symbol's tree strategy when in scope, and a
// park of the first symbol.
func (r *runCtx) stackMembers() string {
	have := map[string]bool{}
	for _, s := range r.strategies {
		have[s] = true
	}
	members := []string{r.primary}
	for _, sym := range r.symbols {
		if id := strings.ToLower(sym) + "_tree"; have[id] {
			members = append(members, id)
		}
	}
	members = append(members, "park-"+strings.ToLower(r.symbols[0]))
	return strings.Join(members, "+")
}

func stepStack(r *runCtx) error {
	bt := backtest.DefaultConfig()
	bt.Db = r.cfg.MarketDB
	bt.Strategy = r.stackMembers()
	bt.Alloc = 0.1
	bt.RunID = r.runID
	bt.Force = true
	return backtest.Run(bt)
}

func stepStackEval(r *runCtx) error {
	var secondary []string
	for _, s := range r.strategies {
		if s != r.primary {
			secondary = append(secondary, s)
		}
	}
	secondary = append(secondary, "park-"+strings.ToLower(r.cfg.Park))
	bt := backtest.DefaultConfig()
	bt.Db = r.cfg.MarketDB
	bt.Mode = "stack-eval"
	bt.Primary = r.primary
	bt.Secondary = strings.Join(secondary, ",")
	bt.StackDepth = 3
	bt.PersistBest = true
	bt.RunID = r.runID
	return backtest.Run(bt)
}

func stepCoveredCall(r *runCtx) error {
	bt := backtest.DefaultConfig()
	bt.Db = r.cfg.MarketDB
	bt.Mode = "covered-call"
	bt.Symbol = r.symbols[0]
	bt.Otm = 2
	bt.Commission = 0.65
	bt.OptSlip = 0.05
	return backtest.Run(bt)
}

// --- compare ---

func stepParkSweep(r *runCtx) error {
	ids := park_sweep.ParseIDs(r.ids())
	sweep := filepath.Join(r.dir, "park_sweep.db")
	report := filepath.Join(r.dir, "park_sweep_report.db")
	html := filepath.Join(r.dir, "park_sweep.html")
	counts, err := park_sweep.Seed(sweep, r.cfg.RefDB, r.cfg.MarketDB, ids)
	if err != nil {
		return err
	}
	for _, c := range counts {
		r.printf("  %-14s %6d rows   %6d pending   %6d skipped\n", c.Kind, c.Rows, c.Pending, c.Skipped)
	}
	if err := park_sweep.Run(sweep, r.cfg.MarketDB, runtime.NumCPU(), ids); err != nil {
		return err
	}
	if err := park_sweep.Rank(sweep, ids); err != nil {
		return err
	}
	return park_sweep.WriteReport(sweep, report, html, ids)
}

func stepScoreboard(r *runCtx) error {
	var errs []error
	for _, mode := range []string{"", "compile", "status"} {
		sc := scoreboard.DefaultConfig()
		sc.Mode = mode
		sc.RunID = r.runID
		sc.Strategy = r.ids()
		sc.Force = mode == ""
		sc.Start = storage.DefaultStartDate
		errs = append(errs, scoreboard.Run(sc))
	}
	return join(errs...)
}

func stepStale(r *runCtx) error {
	gs := gridConfig(r)
	gs.Subcommand = "stale"
	gs.Strategy = r.ids()
	return gridsearch.Run(gs)
}
