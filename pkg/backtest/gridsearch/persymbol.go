package gridsearch

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/rotation_strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

// tradesPerYear is the trade floor of a per-symbol sweep: a setting needs this
// many trades for each year of the window it is judged on (in sample to be
// chosen, held out to be stored), so fewer than that is noise, not evidence.
const tradesPerYear = 4

// tradesFor is the trade floor for a window of the given number of years.
func tradesFor(years float64) int { return max(int(math.Ceil(tradesPerYear*years-1e-9)), 1) }

// persymbolBase is the shared parameter set every symbol's tuned parameters must
// beat out of sample: weekly entries, a limit 10% under the previous close, hold
// 2 sessions, take profit 5%, no stop.
var persymbolBase = gridKey{period: "1w", limit: 0.9, hold: 2, tp: 0.05, sl: 0}

type gridKey struct {
	period    string
	limit, tp float64
	sl        float64
	hold      int
}

func keyOf(r gridResult) gridKey {
	return gridKey{r.Period, r.EntryLimit, r.TakeProfit, r.StopLoss, r.HoldDays}
}

// symbolOutcome is one symbol's tuned parameters and how they did in and out of sample.
type symbolOutcome struct {
	Symbol   string
	Sessions int
	Best     gridKey
	IS, OOS  *gridResult // chosen parameters, in-sample and held-out
	BaseIS   *gridResult // shared base parameters
	BaseOOS  *gridResult
	MinOOS   int // held-out trades required to pass
	Err      error
}

func (o symbolOutcome) oosCAGR() float64 {
	if o.OOS == nil {
		return 0
	}
	return o.OOS.Report.CAGR
}

func (o symbolOutcome) oosTrades() int {
	if o.OOS == nil {
		return 0
	}
	return o.OOS.Report.TotalTrades
}

func (o symbolOutcome) baseOOSCAGR() float64 {
	if o.BaseOOS == nil {
		return 0
	}
	return o.BaseOOS.Report.CAGR
}

// passes reports whether the tuned parameters earn a stored row: enough held-out
// trades, a profit, and a better held-out CAGR than the shared base parameters.
func (o symbolOutcome) passes() bool {
	return o.Err == nil && o.OOS != nil && o.oosTrades() >= max(o.MinOOS, 1) && o.oosCAGR() > 0 && o.oosCAGR() > o.baseOOSCAGR()
}

// smoothedBest picks the parameters whose resilience score, averaged with that
// of their neighbours one step away on each axis, is highest. A lone spike with
// weak neighbours loses to a plateau.
func smoothedBest(results []gridResult, axes map[string][]float64, holds []int) (gridResult, bool) {
	if len(results) == 0 {
		return gridResult{}, false
	}
	byKey := make(map[gridKey]gridResult, len(results))
	for _, r := range results {
		byKey[keyOf(r)] = r
	}
	idx := func(vals []float64, v float64) int {
		for i, x := range vals {
			if math.Abs(x-v) < 1e-9 {
				return i
			}
		}
		return -1
	}
	at := func(vals []float64, i int) (float64, bool) {
		if i < 0 || i >= len(vals) {
			return 0, false
		}
		return vals[i], true
	}
	var best gridResult
	bestScore, found := math.Inf(-1), false
	for _, r := range results {
		sum, n := resilienceScore(r.Report), 1
		add := func(k gridKey) {
			if nb, ok := byKey[k]; ok {
				sum += resilienceScore(nb.Report)
				n++
			}
		}
		k := keyOf(r)
		for _, d := range []int{-1, 1} {
			if v, ok := at(axes["limit"], idx(axes["limit"], k.limit)+d); ok {
				nk := k
				nk.limit = v
				add(nk)
			}
			if v, ok := at(axes["tp"], idx(axes["tp"], k.tp)+d); ok {
				nk := k
				nk.tp = v
				add(nk)
			}
			if v, ok := at(axes["sl"], idx(axes["sl"], k.sl)+d); ok {
				nk := k
				nk.sl = v
				add(nk)
			}
			for i, h := range holds {
				if h == k.hold && i+d >= 0 && i+d < len(holds) {
					nk := k
					nk.hold = holds[i+d]
					add(nk)
				}
			}
		}
		if score := sum / float64(n); score > bestScore {
			best, bestScore, found = r, score, true
		}
	}
	return best, found
}

// symbolsWithHistory lists the symbols (ETFs only unless allSymbols) that have at
// least minSessions derived sessions, most history first.
func symbolsWithHistory(db *sqlx.DB, minSessions int, allSymbols bool) (map[string]int, error) {
	var rows []struct {
		Symbol string `db:"symbol"`
		N      int    `db:"n"`
	}
	if err := db.Select(&rows, `SELECT symbol, COUNT(*) AS n FROM backtest_start
		WHERE length(Date) = 10 AND timeframe = '1d' GROUP BY symbol HAVING n >= ?`, minSessions); err != nil {
		return nil, err
	}
	etf := map[string]bool{}
	if !allSymbols {
		udb, err := storage.OpenSQLite(appenv.UniverseDB())
		if err != nil {
			return nil, err
		}
		defer udb.Close()
		var syms []string
		if err := udb.Select(&syms, `SELECT symbol FROM universe WHERE is_etf = 1`); err != nil {
			return nil, fmt.Errorf("read ETF flags from %s: %w", appenv.UniverseDB(), err)
		}
		for _, s := range syms {
			etf[s] = true
		}
	}
	out := map[string]int{}
	for _, r := range rows {
		if allSymbols || etf[r.Symbol] {
			out[r.Symbol] = r.N
		}
	}
	return out, nil
}

// perSymbolRow is the rotation row that trades one symbol alone with every
// position the whole account, at the shared base parameters (the sweep varies them).
func perSymbolRow(sym string) refdb.RotationStrategy {
	return refdb.RotationStrategy{
		ID: "persym-" + strings.ToLower(sym), Name: sym + " per-symbol optimum",
		Symbols: sym, UniverseSize: 1, TopK: 1, MaxWeightPct: 1.0, RegimeSymbol: "QQQ",
		AllocationPct: 1.0, SlippagePct: 0.0005, Period: persymbolBase.period, Side: "long", Pick: "all",
		EntryLimitPct: persymbolBase.limit, TakeProfitPct: persymbolBase.tp, HoldDays: persymbolBase.hold,
		StopLossPct: persymbolBase.sl, Cooldown: "month",
	}
}

// sweepSymbol tunes one symbol in sample, then reads its chosen and base
// parameters out of a second sweep over the held-out window.
func sweepSymbol(db *sqlx.DB, sym string, sessions, minTrades, minOOS int, isOpts, oosOpts sweepOptions) symbolOutcome {
	out := symbolOutcome{Symbol: sym, Sessions: sessions, MinOOS: minOOS}
	strat, err := rotation_strategy.FromRow(perSymbolRow(sym))
	if err != nil {
		out.Err = err
		return out
	}
	is, err := runSweep(db, strat, isOpts)
	if err != nil {
		out.Err = err
		return out
	}
	space := is.ParamSpace
	axes := map[string][]float64{"limit": sortedCopy(space.EntryLimits), "tp": sortedCopy(space.TakeProfits), "sl": sortedCopy(space.StopLosses)}
	holds := append([]int(nil), space.HoldDays...)
	sort.Ints(holds)
	var enough []gridResult
	for _, r := range is.Results {
		if r.Report.TotalTrades >= minTrades {
			enough = append(enough, r)
		}
	}
	best, ok := smoothedBest(enough, axes, holds)
	if !ok {
		out.Err = fmt.Errorf("no configuration reached %d in-sample trades", minTrades)
		return out
	}
	out.Best = keyOf(best)
	out.IS = &best
	for i := range is.Results {
		if keyOf(is.Results[i]) == persymbolBase {
			r := is.Results[i]
			out.BaseIS = &r
		}
	}
	if oosOpts.StartDate == "" {
		return out
	}
	oos, err := runSweep(db, strat, oosOpts)
	if err != nil {
		return out // no held-out bars or signals: the symbol keeps no held-out result
	}
	for i := range oos.Results {
		r := oos.Results[i]
		switch keyOf(r) {
		case out.Best:
			out.OOS = &r
		}
		if keyOf(r) == persymbolBase {
			b := r
			out.BaseOOS = &b
		}
	}
	return out
}

func sortedCopy(v []float64) []float64 {
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	return c
}

const persymbolSchema = `CREATE TABLE IF NOT EXISTS persymbol_results (
	symbol TEXT PRIMARY KEY, sessions INTEGER, period TEXT, entry_limit REAL, hold_days INTEGER,
	take_profit REAL, stop_loss REAL,
	is_cagr REAL, is_dd REAL, is_win_rate REAL, is_trades INTEGER,
	oos_cagr REAL, oos_dd REAL, oos_win_rate REAL, oos_trades INTEGER,
	base_is_cagr REAL, base_oos_cagr REAL, base_oos_trades INTEGER,
	passed INTEGER, error TEXT, run_at TEXT)`

func savePerSymbol(path string, outs []symbolOutcome) error {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(persymbolSchema); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, o := range outs {
		var isR, oosR, bIS, bOOS struct {
			cagr, dd, wr float64
			trades       int
		}
		fill := func(dst *struct {
			cagr, dd, wr float64
			trades       int
		}, r *gridResult) {
			if r != nil {
				dst.cagr, dst.dd, dst.wr, dst.trades = r.Report.CAGR, r.Report.MaxDrawdownPct, r.Report.WinRate, r.Report.TotalTrades
			}
		}
		fill(&isR, o.IS)
		fill(&oosR, o.OOS)
		fill(&bIS, o.BaseIS)
		fill(&bOOS, o.BaseOOS)
		errText := ""
		if o.Err != nil {
			errText = o.Err.Error()
		}
		if _, err := db.Exec(`INSERT OR REPLACE INTO persymbol_results VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			o.Symbol, o.Sessions, o.Best.period, o.Best.limit, o.Best.hold, o.Best.tp, o.Best.sl,
			isR.cagr, isR.dd, isR.wr, isR.trades, oosR.cagr, oosR.dd, oosR.wr, oosR.trades,
			bIS.cagr, bOOS.cagr, bOOS.trades, boolInt(o.passes()), errText, now); err != nil {
			return err
		}
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// writeSymbolRows stores one rotation_strategy row per passing symbol in the
// reference database, replacing an earlier row of the same id.
func writeSymbolRows(refPath string, outs []symbolOutcome) (int, error) {
	db, err := storage.OpenSQLite(refPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	n := 0
	for _, o := range outs {
		if !o.passes() {
			continue
		}
		r := perSymbolRow(o.Symbol)
		r.Period, r.EntryLimitPct, r.HoldDays, r.TakeProfitPct, r.StopLossPct = o.Best.period, o.Best.limit, o.Best.hold, o.Best.tp, o.Best.sl
		r.Name = fmt.Sprintf("%s per-symbol optimum (%s, limit %.0f%%, hold %dd, TP %.0f%%, SL %.0f%%)",
			o.Symbol, o.Best.period, o.Best.limit*100, o.Best.hold, o.Best.tp*100, o.Best.sl*100)
		if _, err := db.NamedExec(`INSERT OR REPLACE INTO rotation_strategy
			(id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct, regime_symbol, regime_sma, allocation_pct, cash_yield, slippage_pct,
			 period, side, pick, entry_limit_pct, take_profit_pct, hold_days, stop_loss_pct, cooldown)
			VALUES (:id, :name, :symbols, :universe_size, :top_k, :exit_buffer, :max_weight_pct, :regime_symbol, :regime_sma, :allocation_pct, :cash_yield, :slippage_pct,
			 :period, :side, :pick, :entry_limit_pct, :take_profit_pct, :hold_days, :stop_loss_pct, :cooldown)`, r); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	return c[len(c)/2]
}

// runPerSymbol is `gridsearch persymbol`: tune the rotation parameters of every
// symbol with enough history on its own, in sample, then test each symbol's choice
// on the held-out months against the shared base parameters.
func runPerSymbol(conf Config) error {
	db, err := storage.OpenSQLite(conf.Db)
	if err != nil {
		return err
	}
	defer db.Close()

	runner.PrintMarketSourcesFor(os.Stdout, conf.Db, "backtest_start", true)
	if conf.End == "" {
		end, ok, err := storage.InSampleEnd(conf.Db, "backtest_start", conf.Start, "", conf.HoldoutMonths)
		if err != nil {
			return fmt.Errorf("gridsearch persymbol: holdout window: %w", err)
		}
		if ok {
			conf.End = end
		}
	}

	minSessions := conf.MinSessions
	syms, err := symbolsWithHistory(db, minSessions, conf.AllSymbols)
	if err != nil {
		return err
	}
	if conf.Symbol != "" {
		want := map[string]bool{}
		for _, s := range strings.Split(conf.Symbol, ",") {
			want[strings.ToUpper(strings.TrimSpace(s))] = true
		}
		for s := range syms {
			if !want[s] {
				delete(syms, s)
			}
		}
	}
	if len(syms) == 0 {
		return fmt.Errorf("gridsearch persymbol: no symbol has %d or more sessions in %s", minSessions, conf.Db)
	}
	names := make([]string, 0, len(syms))
	for s := range syms {
		names = append(names, s)
	}
	sort.Strings(names)

	minTrades := conf.MinTrades
	if !conf.Passed["min-trades"] {
		minTrades = 1
		if from, err := time.Parse("2006-01-02", conf.Start); err == nil {
			if to, err := time.Parse("2006-01-02", conf.End); err == nil {
				minTrades = tradesFor(to.Sub(from).Hours() / 24 / 365.25)
			}
		}
	}
	minOOS := tradesFor(float64(conf.HoldoutMonths) / 12)
	isOpts := sweepOptions{Capital: conf.Capital, MinTrades: 1, TopN: 1, StartDate: conf.Start, EndDate: conf.End, MarketDB: conf.Db, InnerWorkers: 2}
	oosOpts := isOpts
	oosOpts.MinTrades = 1
	oosOpts.StartDate, oosOpts.EndDate = "", ""
	if conf.End != "" {
		t, err := time.Parse("2006-01-02", conf.End)
		if err != nil {
			return fmt.Errorf("gridsearch persymbol: bad end date %q", conf.End)
		}
		oosOpts.StartDate = t.AddDate(0, 0, 1).Format("2006-01-02")
	}
	fmt.Printf("Per-symbol sweep of %d symbols with %d+ sessions: in-sample %s → %s, held out from %s, at least %d in-sample trades (%d a year) and %d held out.\n",
		len(names), minSessions, conf.Start, conf.End, oosOpts.StartDate, minTrades, tradesPerYear, minOOS)

	// Load the family axes once before the workers start: each worker would
	// otherwise seed them at the same moment and lock the reference database.
	strategy.LoadFamilyAxes("rotation")
	workers := max(conf.Concurrency/2, 1)
	jobs := make(chan string)
	var mu sync.Mutex
	var outs []symbolOutcome
	done := 0
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sym := range jobs {
				o := sweepSymbol(db, sym, syms[sym], minTrades, minOOS, isOpts, oosOpts)
				mu.Lock()
				outs = append(outs, o)
				done++
				switch {
				case o.Err != nil:
					fmt.Printf("[%d/%d] %-6s skipped: %v\n", done, len(names), sym, o.Err)
				default:
					fmt.Printf("[%d/%d] %-6s %s limit %.0f%% hold %dd TP %.0f%% SL %.0f%%   in-sample CAGR %.1f%%   held-out CAGR %.1f%% (%d trades)\n",
						done, len(names), sym, o.Best.period, o.Best.limit*100, o.Best.hold, o.Best.tp*100, o.Best.sl*100,
						o.IS.Report.CAGR*100, o.oosCAGR()*100, o.oosTrades())
				}
				mu.Unlock()
			}
		}()
	}
	for _, s := range names {
		jobs <- s
	}
	close(jobs)
	wg.Wait()
	sort.Slice(outs, func(i, j int) bool { return outs[i].Symbol < outs[j].Symbol })

	if err := savePerSymbol(conf.GridsearchDb, outs); err != nil {
		return fmt.Errorf("save persymbol_results: %w", err)
	}
	printPerSymbolSummary(outs, conf.GridsearchDb)
	if conf.WriteRows {
		n, err := writeSymbolRows(refdb.DefaultPath, outs)
		if err != nil {
			return fmt.Errorf("write rows: %w", err)
		}
		fmt.Printf("\nWrote %d rotation_strategy rows (ids persym-<symbol>) to %s.\n", n, refdb.DefaultPath)
	} else {
		fmt.Println("\nNo rows written. Add -write-rows to store the passing symbols as rotation_strategy rows.")
	}
	return nil
}

func printPerSymbolSummary(outs []symbolOutcome, gridDB string) {
	ranked := make([]symbolOutcome, 0, len(outs))
	var tunedOOS, baseOOS, tunedIS, baseIS []float64
	pass, skipped := 0, 0
	for _, o := range outs {
		if o.Err != nil || o.IS == nil {
			skipped++
			continue
		}
		ranked = append(ranked, o)
		tunedOOS = append(tunedOOS, o.oosCAGR())
		baseOOS = append(baseOOS, o.baseOOSCAGR())
		tunedIS = append(tunedIS, o.IS.Report.CAGR)
		if o.BaseIS != nil {
			baseIS = append(baseIS, o.BaseIS.Report.CAGR)
		}
		if o.passes() {
			pass++
		}
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].oosCAGR() > ranked[j].oosCAGR() })

	fmt.Printf("\nPER-SYMBOL OPTIMA, ranked by held-out CAGR (parameters chosen in sample only):\n")
	fmt.Printf("  %-6s %-4s %6s %5s %5s %5s | %8s %7s %6s | %8s %7s %6s | %8s\n",
		"SYMBOL", "per", "limit", "hold", "TP", "SL", "IS CAGR", "IS DD", "IS n", "OOS CAGR", "OOS DD", "OOS n", "BASE OOS")
	for _, o := range ranked {
		mark := " "
		if o.passes() {
			mark = "*"
		}
		oosDD := 0.0
		if o.OOS != nil {
			oosDD = o.OOS.Report.MaxDrawdownPct * 100
		}
		fmt.Printf("%s %-6s %-4s %5.0f%% %4dd %4.0f%% %4.0f%% | %7.1f%% %6.1f%% %6d | %7.1f%% %6.1f%% %6d | %7.1f%%\n",
			mark, o.Symbol, o.Best.period, o.Best.limit*100, o.Best.hold, o.Best.tp*100, o.Best.sl*100,
			o.IS.Report.CAGR*100, o.IS.Report.MaxDrawdownPct*100, o.IS.Report.TotalTrades,
			o.oosCAGR()*100, oosDD, o.oosTrades(), o.baseOOSCAGR()*100)
	}
	if len(ranked) == 0 {
		fmt.Printf("\nNo symbol reached the in-sample trade floor (%d skipped).\n", skipped)
		return
	}
	fmt.Printf("\nDOES TUNING PER SYMBOL HELP? (median CAGR over %d symbols; %d skipped)\n", len(ranked), skipped)
	fmt.Printf("  in-sample : tuned %6.1f%%   shared base %6.1f%%\n", median(tunedIS)*100, median(baseIS)*100)
	fmt.Printf("  held-out  : tuned %6.1f%%   shared base %6.1f%%\n", median(tunedOOS)*100, median(baseOOS)*100)
	better := 0
	for _, o := range ranked {
		if o.oosCAGR() > o.baseOOSCAGR() {
			better++
		}
	}
	fmt.Printf("  tuned beat the base held-out for %d of %d symbols; %d pass (>= %d held-out trades, profit, beat base; marked *)\n",
		better, len(ranked), pass, ranked[0].MinOOS)
	fmt.Printf("Results table persymbol_results in %s\n", gridDB)
	_ = os.Stdout.Sync()
}
