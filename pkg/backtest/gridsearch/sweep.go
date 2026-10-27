package gridsearch

import (
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/hoursim"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/jmoiron/sqlx"
)

// sweepOptions bundles per-run overrides and controls shared by single-strategy
// and batch grid-search modes.
type sweepOptions struct {
	AllocOverride     *float64
	CashYieldOverride *float64
	SymbolOverride    []string // trade-symbol universe override (one or many)
	SignalOverride    string
	Capital           float64
	MinTrades         int
	TopN              int
	StartDate         string   // earliest bar date to sweep ("" = full history)
	EndDate           string   // latest bar date to sweep ("" = latest bar)
	InnerWorkers      int      // single-strategy mode only: workers within this one sweep
	MarketDB          string   // market database path; streak sweeps calculate entries in SQL from it
	PeriodOverride    []string // -period: calendar periods to sweep instead of the strategy's own axis
}

// sweepTask is one (signal-days, hold, TP, SL, regime, allocation, symbol)
// permutation to evaluate.
type sweepTask struct {
	sym        string
	sigDays    int
	hold       int
	tp         float64
	sl         float64
	regime     string
	alloc      float64
	tradeBars  []models.Bar
	isBaseline bool
	period     string  // calendar period of a period sweep ("" for any other)
	limit      float64 // buy limit as a fraction of the previous close in a period sweep (0 = none)
}

// sweepContext holds everything about one strategy's parameter space needed to
// evaluate its tasks, computed once up front (bar fetches, tree-bounce base
// signals) and then reused across every task.
type sweepContext struct {
	Strat       strategy.Strategy
	ParamSpace  strategy.ParameterSpace
	SignalBars  []models.Bar
	BaseSignals []models.Signal                    // precomputed once for tree_bounce strategies
	Entries     map[streakEntryKey][]models.Signal // streak entries calculated once in SQL, exits applied per grid point
	SortedDates []string
	TotalPerms  int
	StartedAt   time.Time

	// Period sweeps: the strategy's signals for each period, calculated once in
	// SQL, and the bars they trade.
	PeriodSignals map[string][]models.Signal
	PeriodConfigs map[string]strategy.StrategyConfig
	AllBars       map[string][]models.Bar

	// Hourly bars every grid point is played on (nil: each session is played as its daily bar).
	// Read-only, shared by the workers.
	Intraday map[string][]models.Bar
}

// estimatePerms computes a strategy's generic parameter grid size without
// fetching any bars — cheap enough to call before deciding whether a strategy
// is worth sweeping at all in batch mode (see -max-perms).
func estimatePerms(strat strategy.Strategy, opts sweepOptions) int {
	paramSpace := strategy.AssessParameterSpace(strat)
	if opts.AllocOverride != nil {
		paramSpace.Allocations = []float64{*opts.AllocOverride}
	}
	if len(opts.SymbolOverride) > 0 {
		paramSpace.Symbols = opts.SymbolOverride
	}
	if len(opts.PeriodOverride) > 0 && len(paramSpace.Periods) > 0 {
		paramSpace.Periods = opts.PeriodOverride
	}
	return paramSpace.Perms()
}

// prepareSweep resolves a strategy's parameter space, fetches the bars it needs,
// and builds the full list of tasks to evaluate — but evaluates nothing. This is
// the part of a sweep that's cheap (I/O, not simulation), so both single-strategy
// mode and the batch flattened pool call it once per strategy up front.
func prepareSweep(db *sqlx.DB, strat strategy.Strategy, opts sweepOptions) (*sweepContext, []sweepTask, error) {
	paramSpace := strategy.AssessParameterSpace(strat)
	if opts.AllocOverride != nil {
		paramSpace.Allocations = []float64{*opts.AllocOverride}
	}
	if opts.CashYieldOverride != nil {
		paramSpace.CashYield = *opts.CashYieldOverride
	}
	if len(opts.SymbolOverride) > 0 {
		paramSpace.Symbols = opts.SymbolOverride
	}
	if opts.SignalOverride != "" {
		paramSpace.SignalSymbol = opts.SignalOverride
	}

	if len(paramSpace.Periods) > 0 {
		if len(opts.PeriodOverride) > 0 {
			paramSpace.Periods = opts.PeriodOverride
		}
		return preparePeriodSweep(db, strat, paramSpace, opts)
	}

	barMap, _, err := storage.FetchBars(db, "backtest_start", []string{paramSpace.SignalSymbol}, opts.StartDate, opts.EndDate)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch %s bars: %w", paramSpace.SignalSymbol, err)
	}
	signalBars := barMap[paramSpace.SignalSymbol]
	if len(signalBars) == 0 {
		return nil, nil, fmt.Errorf("no price bars found for signal symbol %s", paramSpace.SignalSymbol)
	}

	tradeBarsMap := make(map[string][]models.Bar, len(paramSpace.Symbols))
	for _, sym := range paramSpace.Symbols {
		tBarMap, _, err := storage.FetchBars(db, "backtest_start", []string{sym}, opts.StartDate, opts.EndDate)
		bars := tBarMap[sym]
		if err == nil && len(bars) > 0 {
			tradeBarsMap[sym] = bars
		}
	}

	dateSet := make(map[string]struct{})
	for _, b := range signalBars {
		dateSet[b.Date] = struct{}{}
	}
	for _, bars := range tradeBarsMap {
		for _, b := range bars {
			dateSet[b.Date] = struct{}{}
		}
	}
	sortedDates := make([]string, 0, len(dateSet))
	for d := range dateSet {
		sortedDates = append(sortedDates, d)
	}
	sort.Strings(sortedDates)

	totalPerms := paramSpace.Perms()

	var baseSignals []models.Signal
	if paramSpace.EntriesFixed() {
		// Strategies whose entry rule reads one symbol but trades another (e.g. VOO
		// volume → TQQQ) also need the trade symbol's bars to price the signal.
		// Same-symbol tree strategies (MARA→MARA) add nothing here.
		baseInput := map[string][]models.Bar{paramSpace.SignalSymbol: signalBars}
		for sym, bars := range tradeBarsMap {
			if _, dup := baseInput[sym]; !dup {
				baseInput[sym] = bars
			}
		}
		// The strategy's own pipeline decides the entries, once. It reads the market
		// database and writes its slice tables to a scratch calc database.
		if opts.MarketDB == "" {
			return nil, nil, fmt.Errorf("%s: a fixed-entry sweep needs the market database path", strat.ID())
		}
		calcPath, cleanup := runner.CalcDBPath(appenv.Reports(), "gridsearch_base_"+strat.ID())
		strat.SetDatabases(opts.MarketDB, calcPath)
		baseSignals = strat.GenerateSignals(baseInput)
		cleanup()
		if len(baseSignals) == 0 {
			return nil, nil, fmt.Errorf("%s produced no entries to search exits over (no saved model for it? run `train markov`)", strat.ID())
		}
	}

	var entries map[streakEntryKey][]models.Signal
	if !paramSpace.EntriesFixed() {
		var tradeSyms []string
		for _, sym := range paramSpace.Symbols {
			if _, ok := tradeBarsMap[sym]; ok {
				tradeSyms = append(tradeSyms, sym)
			}
		}
		var err error
		entries, err = buildStreakEntries(opts.MarketDB, strat.ID(), paramSpace.SignalSymbol, paramSpace.Direction,
			opts.StartDate, opts.EndDate, tradeSyms, paramSpace.SignalDays, paramSpace.Regimes)
		if err != nil {
			return nil, nil, fmt.Errorf("streak entries for %s: %w", strat.ID(), err)
		}
	}

	ctx := &sweepContext{
		Entries:     entries,
		Strat:       strat,
		ParamSpace:  paramSpace,
		SignalBars:  signalBars,
		BaseSignals: baseSignals,
		SortedDates: sortedDates,
		TotalPerms:  totalPerms,
		StartedAt:   time.Now(),
	}
	if ctx.Intraday, err = hourlyFor(opts.MarketDB, append([]string{paramSpace.SignalSymbol}, paramSpace.Symbols...)); err != nil {
		return nil, nil, fmt.Errorf("%s: hourly bars: %w", strat.ID(), err)
	}

	var tasks []sweepTask
	for _, sym := range paramSpace.Symbols {
		tBars, ok := tradeBarsMap[sym]
		if !ok {
			continue
		}
		for _, sigDays := range paramSpace.SignalDays {
			for _, regime := range paramSpace.Regimes {
				for _, hold := range paramSpace.HoldDays {
					for _, tp := range paramSpace.TakeProfits {
						for _, sl := range paramSpace.StopLosses {
							for _, alloc := range paramSpace.Allocations {
								isBase := false
								if paramSpace.EntriesFixed() {
									isBase = (hold == paramSpace.Baseline.HoldDays &&
										math.Abs(tp-paramSpace.Baseline.TakeProfit) < 1e-4 &&
										math.Abs(sl-paramSpace.Baseline.StopLoss) < 1e-4)
								} else {
									isBase = (sigDays == paramSpace.Baseline.SignalDays &&
										hold == paramSpace.Baseline.HoldDays &&
										math.Abs(tp-paramSpace.Baseline.TakeProfit) < 1e-4 &&
										math.Abs(sl-paramSpace.Baseline.StopLoss) < 1e-4 &&
										regime == paramSpace.Baseline.Regime)
								}
								tasks = append(tasks, sweepTask{
									sym: sym, sigDays: sigDays, hold: hold, tp: tp, sl: sl,
									regime: regime, alloc: alloc, tradeBars: tBars, isBaseline: isBase,
								})
							}
						}
					}
				}
			}
		}
	}

	return ctx, tasks, nil
}

// evaluateTask runs the simulation for a single task and returns the result. ok
// is false when the task was filtered out by the minimum trade count (either by
// raw signal count or by the simulator's actual fill count) and should not be
// counted as a result.
func evaluateTask(ctx *sweepContext, t sweepTask, opts sweepOptions) (gridResult, bool) {
	// Trades and the daily equity curve are only kept for the baseline; every
	// other result is slim (the full grid is far too big to hold in memory at
	// 100k+ permutations) and finalizeSweep re-simulates the few winners.
	return evalTask(ctx, t, opts, t.isBaseline)
}

func evalTask(ctx *sweepContext, t sweepTask, opts sweepOptions, keepDetail bool) (gridResult, bool) {
	if t.period != "" {
		return evalPeriodTask(ctx, t, opts, keepDetail)
	}
	barsBySymbol := map[string][]models.Bar{
		ctx.ParamSpace.SignalSymbol: ctx.SignalBars,
		t.sym:                       t.tradeBars,
	}

	cfg := strategy.StrategyConfig{
		AllocationPct:   t.alloc,
		TakeProfitPct:   t.tp,
		StopLossPct:     stopMultiplier(t.sl),
		HoldingWindow:   t.hold,
		PositionCap:     1,
		CashYieldAnnual: ctx.ParamSpace.CashYield,
	}

	var sigs []models.Signal
	if ctx.ParamSpace.EntriesFixed() {
		sigs = make([]models.Signal, len(ctx.BaseSignals))
		for idx, bs := range ctx.BaseSignals {
			sCopy := bs
			sCopy.HoldDaysOverride = t.hold
			if t.tp > 0 {
				sCopy.TakeProfit = bs.Close * (1.0 + t.tp)
			} else {
				sCopy.TakeProfit = 0
			}
			if t.sl > 0 {
				sCopy.StopLoss = bs.Close * (1.0 - t.sl)
			} else {
				sCopy.StopLoss = 0
			}
			sigs[idx] = sCopy
		}
	} else {
		sigs = streakSignalsFor(ctx.Entries[streakEntryKey{t.sym, t.sigDays, t.regime}], t.tp, t.sl, t.hold, t.sym+"-opt")
	}

	if len(sigs) < opts.MinTrades {
		return gridResult{}, false
	}

	report, trades, curve, ok := ctx.play(cfg, sigs, barsBySymbol, opts.Capital)
	if !ok {
		return gridResult{}, false
	}

	if report.TotalTrades < opts.MinTrades {
		return gridResult{}, false
	}

	label := ""
	signalDays := t.sigDays
	regime := t.regime
	if ctx.ParamSpace.EntriesFixed() {
		label = fmt.Sprintf("%s/Hold-%dd/TP+%.0f%%/SL-%.0f%%", t.sym, t.hold, t.tp*100, t.sl*100)
		signalDays = 0 // tree_bounce's SignalDays axis is a fixed [1] placeholder, not a real decline-day window
		regime = ""    // tree_bounce entries aren't regime-gated
	} else {
		label = fmt.Sprintf("%s/%dd/%dd/+%.0f%%-%.0f%%/%s", t.sym, t.sigDays, t.hold, t.tp*100, t.sl*100, t.regime)
	}

	if !keepDetail {
		trades, curve = nil, nil
	}
	return gridResult{
		task:         t,
		Label:        label,
		Report:       report,
		Trades:       trades,
		Curve:        curve,
		IsBaseline:   t.isBaseline,
		Symbol:       t.sym,
		SignalSymbol: ctx.ParamSpace.SignalSymbol,
		SignalDays:   signalDays,
		HoldDays:     t.hold,
		TakeProfit:   t.tp,
		StopLoss:     t.sl,
		Regime:       regime,
		Allocation:   t.alloc,
	}, true
}

// finalizeSweep ranks a strategy's completed results three ways and packages
// everything into a sweepOutcome.
func finalizeSweep(ctx *sweepContext, results []gridResult, baselineRes *gridResult, opts sweepOptions) sweepOutcome {
	outcome := sweepOutcome{
		Strat:       ctx.Strat,
		ParamSpace:  ctx.ParamSpace,
		SignalBars:  ctx.SignalBars,
		TotalPerms:  ctx.TotalPerms,
		Results:     results,
		BaselineRes: baselineRes,
		Elapsed:     time.Since(ctx.StartedAt),
	}
	if len(results) == 0 {
		return outcome
	}

	topN := opts.TopN
	if topN < 1 {
		topN = 10
	}

	byCalmar := append([]gridResult(nil), results...)
	sort.Slice(byCalmar, func(i, j int) bool { return byCalmar[i].Report.CalmarRatio > byCalmar[j].Report.CalmarRatio })
	if len(byCalmar) > topN {
		byCalmar = byCalmar[:topN]
	}
	outcome.TopCalmar = byCalmar

	byResilience := append([]gridResult(nil), results...)
	sort.Slice(byResilience, func(i, j int) bool {
		return resilienceScore(byResilience[i].Report) > resilienceScore(byResilience[j].Report)
	})
	if len(byResilience) > topN {
		byResilience = byResilience[:topN]
	}
	outcome.TopResilience = byResilience

	byProfit := append([]gridResult(nil), results...)
	sort.Slice(byProfit, func(i, j int) bool { return byProfit[i].Report.NetProfit > byProfit[j].Report.NetProfit })
	if len(byProfit) > topN {
		byProfit = byProfit[:topN]
	}
	outcome.TopProfit = byProfit

	// Re-simulate the winners to restore their trades/equity curves.
	for _, top := range [][]gridResult{outcome.TopCalmar, outcome.TopResilience, outcome.TopProfit} {
		for i := range top {
			if top[i].Curve != nil {
				continue
			}
			if full, ok := evalTask(ctx, top[i].task, opts, true); ok {
				top[i] = full
			}
		}
	}

	return outcome
}

// runSweep executes the full baked-in parameter grid search for one strategy
// using a local worker pool sized by opts.InnerWorkers. This is the
// single-strategy CLI path — batch mode uses prepareSweep/evaluateTask directly
// against one shared pool instead (see runBatchSweepFlat) so slow strategies
// aren't stuck with a small fixed inner-worker count while other cores idle.
func runSweep(db *sqlx.DB, strat strategy.Strategy, opts sweepOptions) (sweepOutcome, error) {
	ctx, tasks, err := prepareSweep(db, strat, opts)
	if err != nil {
		return sweepOutcome{}, err
	}

	taskCh := make(chan sweepTask, len(tasks))
	for _, t := range tasks {
		taskCh <- t
	}
	close(taskCh)

	var results []gridResult
	var baselineRes *gridResult
	var mu sync.Mutex
	var wg sync.WaitGroup

	workers := opts.InnerWorkers
	if workers < 1 {
		workers = 1
	}
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for t := range taskCh {
				res, ok := evaluateTask(ctx, t, opts)
				if !ok {
					continue
				}
				mu.Lock()
				results = append(results, res)
				if t.isBaseline {
					bCopy := res
					baselineRes = &bCopy
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	return finalizeSweep(ctx, results, baselineRes, opts), nil
}

// preparePeriodSweep is prepareSweep for a strategy that reruns its own pipeline
// per calendar period (rotation period rows). The pipeline decides every entry
// and exit, so each period runs once and the grid reprices the same signals. The
// periods are calculated concurrently, one calc database each.
func preparePeriodSweep(db *sqlx.DB, strat strategy.Strategy, space strategy.ParameterSpace, opts sweepOptions) (*sweepContext, []sweepTask, error) {
	pv, ok := strat.(strategy.PeriodVariants)
	if !ok {
		return nil, nil, fmt.Errorf("%s has no period variants", strat.ID())
	}
	if opts.MarketDB == "" {
		return nil, nil, fmt.Errorf("%s: a period sweep needs the market database path", strat.ID())
	}
	bars, _, err := storage.FetchBars(db, "backtest_start", space.Symbols, opts.StartDate, opts.EndDate)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch bars for %s: %w", strat.ID(), err)
	}
	if len(bars) == 0 {
		return nil, nil, fmt.Errorf("%s: no price bars found", strat.ID())
	}
	dateSet := map[string]struct{}{}
	var signalBars []models.Bar
	for _, bs := range bars {
		for _, b := range bs {
			dateSet[b.Date] = struct{}{}
		}
		if len(bs) > len(signalBars) {
			signalBars = bs // the longest history stands in for the data window
		}
	}
	sortedDates := make([]string, 0, len(dateSet))
	for d := range dateSet {
		sortedDates = append(sortedDates, d)
	}
	sort.Strings(sortedDates)

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		signals = map[string][]models.Signal{}
		configs = map[string]strategy.StrategyConfig{}
	)
	for _, period := range space.Periods {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := pv.PeriodVariant(period)
			calcPath, cleanup := runner.CalcDBPath(appenv.Reports(), "gridsearch_period_"+period+"_"+strat.ID())
			defer cleanup()
			v.SetDatabases(opts.MarketDB, calcPath)
			sigs := v.GenerateSignals(bars)
			mu.Lock()
			defer mu.Unlock()
			if len(sigs) == 0 {
				fmt.Printf("   ⚠️  %s: period %s produced no signals, skipped\n", strat.ID(), period)
				return
			}
			signals[period], configs[period] = sigs, v.DefaultConfig()
		}()
	}
	wg.Wait()
	if len(signals) == 0 {
		return nil, nil, fmt.Errorf("%s produced no signals for any of the periods %v", strat.ID(), space.Periods)
	}

	ctx := &sweepContext{
		Strat: strat, ParamSpace: space, SignalBars: signalBars, SortedDates: sortedDates,
		TotalPerms: space.Perms(), StartedAt: time.Now(),
		PeriodSignals: signals, PeriodConfigs: configs, AllBars: bars,
	}
	{
		syms := make([]string, 0, len(bars))
		for sym := range bars {
			syms = append(syms, sym)
		}
		sort.Strings(syms)
		if ctx.Intraday, err = runner.LoadFillBars(opts.MarketDB, syms); err != nil {
			return nil, nil, fmt.Errorf("%s: hourly bars: %w", strat.ID(), err)
		}
	}
	var tasks []sweepTask
	limits := space.EntryLimits
	if len(limits) == 0 {
		limits = []float64{0}
	}
	for _, period := range space.Periods {
		if _, ok := signals[period]; !ok {
			continue
		}
		for _, limit := range limits {
			for _, hold := range space.HoldDays {
				for _, tp := range space.TakeProfits {
					for _, sl := range space.StopLosses {
						for _, alloc := range space.Allocations {
							tasks = append(tasks, sweepTask{
								period: period, limit: limit, hold: hold, tp: tp, sl: sl, alloc: alloc,
								isBaseline: period == space.Baseline.Period && hold == space.Baseline.HoldDays &&
									math.Abs(limit-space.Baseline.EntryLimit) < 1e-9 &&
									math.Abs(tp-space.Baseline.TakeProfit) < 1e-4 && math.Abs(sl-space.Baseline.StopLoss) < 1e-4,
							})
						}
					}
				}
			}
		}
	}
	return ctx, tasks, nil
}

// stopMultiplier converts a grid stop (0.05 = 5% under the entry) to the
// StrategyConfig form, a multiplier of the entry (0.95), with 0 meaning no stop.
// Passing the raw 0.05 puts the stop at 5% of the entry price, which never
// triggers, so every stop on the axis gave the same result.
func stopMultiplier(sl float64) float64 {
	if sl <= 0 {
		return 0
	}
	return 1 - sl
}

// evalPeriodTask simulates one period's signals, repriced for the grid point.
func evalPeriodTask(ctx *sweepContext, t sweepTask, opts sweepOptions, keepDetail bool) (gridResult, bool) {
	cfg := ctx.PeriodConfigs[t.period]
	cfg.AllocationPct = t.alloc
	cfg.TakeProfitPct = t.tp
	cfg.StopLossPct = stopMultiplier(t.sl)
	cfg.HoldingWindow = t.hold
	if t.hold <= 0 {
		cfg.HoldingWindow = 99999 // no hold limit: the period's last session exits
	}
	cfg.EntryLimitPct = t.limit
	cfg.SameDayExit = t.limit > 0 // a limit order fills during the session, so its target can too
	if t.limit > 0 && t.tp > 0 {
		cfg.TargetPct = 0 // the simulator prefers TargetPct above 1 over TakeProfitPct
	}
	cfg.CashYieldAnnual = ctx.ParamSpace.CashYield

	base := ctx.PeriodSignals[t.period]
	sigs := make([]models.Signal, len(base))
	for i, bs := range base {
		sCopy := bs
		sCopy.HoldDaysOverride = max(t.hold, 0)
		sCopy.TakeProfit, sCopy.StopLoss = 0, 0
		// A limit entry books its own price, so the simulator measures the target
		// and stop from that fill (cfg) and not from the signal bar's close.
		if bs.Entry == 1 && t.limit <= 0 {
			if t.tp > 0 {
				sCopy.TakeProfit = bs.Close * (1.0 + t.tp)
			}
			if t.sl > 0 {
				sCopy.StopLoss = bs.Close * (1.0 - t.sl)
			}
		}
		sigs[i] = sCopy
	}
	if len(sigs) < opts.MinTrades {
		return gridResult{}, false
	}
	report, trades, curve, ok := ctx.play(cfg, sigs, ctx.AllBars, opts.Capital)
	if !ok {
		return gridResult{}, false
	}
	if report.TotalTrades < opts.MinTrades {
		return gridResult{}, false
	}
	if !keepDetail {
		trades, curve = nil, nil
	}
	return gridResult{
		task:       t,
		Label:      fmt.Sprintf("Period-%s/Limit-%.0f%%/Hold-%dd/TP+%.0f%%/SL-%.0f%%", t.period, t.limit*100, t.hold, t.tp*100, t.sl*100),
		Report:     report,
		Trades:     trades,
		Curve:      curve,
		IsBaseline: t.isBaseline,
		Period:     t.period,
		EntryLimit: t.limit,
		HoldDays:   t.hold,
		TakeProfit: t.tp,
		StopLoss:   t.sl,
		Allocation: t.alloc,
	}, true
}

// hourlyFor loads the hourly bars of symbols from the market database beside opts.MarketDB; none
// is not an error (the grid plays each session as its daily bar and the log says so).
func hourlyFor(marketDB string, symbols []string) (map[string][]models.Bar, error) {
	seen := map[string]bool{}
	var syms []string
	for _, s := range symbols {
		if s = strings.ToUpper(strings.TrimSpace(s)); s != "" && !seen[s] {
			seen[s] = true
			syms = append(syms, s)
		}
	}
	sort.Strings(syms)
	if _, err := os.Stat(appenv.BarDB(marketDB, "1h")); err != nil {
		return nil, nil
	}
	return runner.LoadFillBars(marketDB, syms)
}

// play runs one grid point on the hourly simulator. A grid point that uses a setting the simulator
// does not model is skipped, and the log says why once.
func (c *sweepContext) play(cfg strategy.StrategyConfig, sigs []models.Signal, daily map[string][]models.Bar, capital float64) (models.PerformanceReport, []models.Trade, []models.DailyEquityPoint, bool) {
	out, err := hoursim.Run(hoursim.Input{Signals: sigs, Daily: daily, Hourly: c.Intraday, Dates: c.SortedDates, Config: cfg, Capital: capital})
	if err != nil {
		skipOnce.Do(func() { log.Printf("gridsearch: grid points skipped: %v", err) })
		return models.PerformanceReport{}, nil, nil, false
	}
	return out.Report, out.Trades, out.Curve, true
}

var skipOnce sync.Once
