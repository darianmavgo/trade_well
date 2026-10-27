// Package reliability says how far a backtest result can be trusted. It looks
// for six recognized problems and reports each as PASS, INFO, WARN or FAIL
// before the numbers are shown:
//
//	INTRABAR    a stop and a target (or a limit and a target) inside one bar, so the order is unknown
//	RESOLUTION  a rule judged on coarser bars than it needs (limit fills on thin or missing hourly sessions)
//	LOOKAHEAD   symptoms of using information from after the decision
//	FILLMODEL   fills that rest on a touch of the limit, with no queue or partial fills
//	DATAGAPS    sessions with no bars for the symbols the strategy trades
//	OVERFIT     in-sample selection: no held-out period, a swept strategy, too few trades,
//	            or a held-out result far below the in-sample one
//
// The checks run on the finished trades and the bars in the market databases;
// they do not change the simulation. A clean report is not proof: look-ahead
// inside a strategy's own signal SQL cannot be seen from trades.
package reliability

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"

	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/barcoverage"
	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/train"
)

// Level is how serious a finding is.
type Level int

const (
	Pass Level = iota
	Info
	Warn
	Fail
)

func (l Level) String() string { return [...]string{"PASS", "INFO", "WARN", "FAIL"}[l] }

// Finding is the result of one check.
type Finding struct {
	Code    string // INTRABAR, RESOLUTION, LOOKAHEAD, FILLMODEL, DATAGAPS, OVERFIT
	Title   string
	Level   Level
	Summary string
	Detail  []string
}

// Input is one finished run to assess.
type Input struct {
	MarketDB      string
	Table         string
	Strategy      strategy.Strategy
	Config        strategy.StrategyConfig // as simulated, overrides applied
	Trades        []models.Trade
	Report        models.PerformanceReport
	HoldoutMonths int
	Phase         string                    // "in-sample", "out-of-sample" or "" for one pass over all history
	Prior         *models.PerformanceReport // the in-sample report, when assessing the out-of-sample pass
}

// Thresholds. A regular session has 7 hourly bars (9:00 to 15:00 Eastern).
const (
	thinSessionBars   = 5      // fewer hourly bars than this in an entry session is thin
	priceTolerance    = 0.002  // a fill this far outside the bar's range is slippage and rounding
	touchOnlyBand     = 0.001  // session low within this of the fill: the limit was only touched
	touchOnlyWarnFrac = 0.25   // share of limit fills that were touch-only before it is a warning
	sessionGapFail    = 0.90   // a symbol with bars on fewer of the sessions than this is a failure
	sessionGapWarn    = 0.97   // and fewer than this a warning
	smallSample       = 30     // fewer trades than this prove little
	fillSlipTolerance = 0.0006 // entry prices carry the configured slippage
)

// Assess runs the six checks. A check that cannot run reports why instead of passing.
func Assess(in Input) []Finding {
	a := &assessor{in: in}
	a.load()
	return []Finding{a.intrabar(), a.resolution(), a.lookahead(), a.fillModel(), a.dataGaps(), a.overfit()}
}

type assessor struct {
	in      Input
	limit   bool // enters on a buy limit
	symbols []string
	daily   map[string]map[string]models.Bar // symbol -> date -> daily bar
	hourly  map[string]map[string][]models.Bar
	loadErr string
}

func (a *assessor) load() {
	a.limit = a.in.Config.EntryLimitPct > 0
	set := map[string]bool{}
	for _, t := range a.in.Trades {
		set[t.Symbol] = true
	}
	for s := range set {
		a.symbols = append(a.symbols, s)
	}
	sort.Strings(a.symbols)

	a.daily = map[string]map[string]models.Bar{}
	db, err := storage.OpenSQLite(a.in.MarketDB)
	if err != nil {
		a.loadErr = err.Error()
		return
	}
	defer db.Close()
	table := a.in.Table
	if table == "" {
		table = "backtest_start"
	}
	bars, _, err := storage.FetchBars(db, table, a.symbols, a.in.Report.StartDate, a.in.Report.EndDate)
	if err != nil {
		a.loadErr = err.Error()
		return
	}
	for sym, bs := range bars {
		m := make(map[string]models.Bar, len(bs))
		for _, b := range bs {
			m[b.Date] = b
		}
		a.daily[sym] = m
	}
	a.hourly = map[string]map[string][]models.Bar{}
	if len(a.symbols) == 0 {
		return
	}
	in, err := runner.LoadIntraday(a.in.MarketDB, a.symbols)
	if err != nil {
		return // no hourly database: every session counts as having no hourly bars
	}
	for sym, bs := range in {
		m := map[string][]models.Bar{}
		for _, b := range bs {
			m[b.Date[:10]] = append(m[b.Date[:10]], b)
		}
		a.hourly[sym] = m
	}
}

func pct(n, d int) string {
	if d == 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(n)/float64(d))
}

// --- 1. INTRABAR ---------------------------------------------------------

func (a *assessor) intrabar() Finding {
	f := Finding{Code: "INTRABAR", Title: "Intrabar ambiguity"}
	if a.loadErr != "" {
		return unchecked(f, a.loadErr)
	}
	var sameDay, sameDayDaily, sameDayDailyProfit, sameHour, multi, multiAmbiguous int
	for _, t := range a.in.Trades {
		if t.TargetPrice <= 0 || t.StopLossPrice <= 0 {
			continue
		}
		if t.EntryDate == t.ExitDate {
			sameDay++
			sess := a.hourly[t.Symbol][t.EntryDate]
			if len(sess) == 0 {
				sameDayDaily++
				if t.ExitReason == models.ExitReasonProfitTarget {
					sameDayDailyProfit++
				}
				continue
			}
			if inOneBar(sess, t) {
				sameHour++
			}
			continue
		}
		multi++
		for d, b := range a.daily[t.Symbol] {
			if d > t.EntryDate && d <= t.ExitDate && b.Low <= t.StopLossPrice && b.High >= t.TargetPrice {
				multiAmbiguous++
				break
			}
		}
	}
	f.Detail = []string{
		fmt.Sprintf("same-session exits: %d; decided on hourly bars: %d; decided on the daily bar alone: %d (of which profit targets: %d)",
			sameDay, sameDay-sameDayDaily, sameDayDaily, sameDayDailyProfit),
		fmt.Sprintf("same-session exits whose stop and target sit in one hourly bar (stop is taken first): %d", sameHour),
		fmt.Sprintf("later-session exits with a day whose range holds both stop and target (stop is taken first): %d of %d", multiAmbiguous, multi),
	}
	switch {
	case sameDayDaily > 0:
		f.Level = Fail
		f.Summary = fmt.Sprintf("%d same-session exits (%s of them) were decided on the daily bar, which cannot say whether the target or the entry came first; %d are profit targets", sameDayDaily, pct(sameDayDaily, sameDay), sameDayDailyProfit)
	case sameHour > 0 || multiAmbiguous > 0:
		f.Level = Info
		f.Summary = fmt.Sprintf("%d trades had stop and target inside one bar; the order is unknown and the stop is taken first (conservative)", sameHour+multiAmbiguous)
	default:
		f.Level = Pass
		f.Summary = "no trade depends on an unknown order of events inside a bar"
	}
	return f
}

// inOneBar reports whether, after the fill, one hourly bar holds both the stop
// and the target. The fill bar's high counts only up to the fill, as in the simulator.
func inOneBar(sess []models.Bar, t models.Trade) bool {
	filled := false
	for _, b := range sess {
		if !filled {
			if b.Low > t.EntryPrice*(1+fillSlipTolerance) {
				continue
			}
			filled = true
			b.High = t.EntryPrice
		}
		if b.Low <= t.StopLossPrice && b.High >= t.TargetPrice {
			return true
		}
	}
	return false
}

// --- 2. RESOLUTION -------------------------------------------------------

func (a *assessor) resolution() Finding {
	f := Finding{Code: "RESOLUTION", Title: "Data resolution"}
	if !a.limit {
		f.Level, f.Summary = Pass, "entries do not depend on intraday order (no limit entries)"
		return f
	}
	if a.loadErr != "" {
		return unchecked(f, a.loadErr)
	}
	var total, full, thin, none int
	for _, t := range a.in.Trades {
		if t.OrderType != "limit" {
			continue
		}
		total++
		switch n := len(a.hourly[t.Symbol][t.EntryDate]); {
		case n == 0:
			none++
		case n < thinSessionBars:
			thin++
		default:
			full++
		}
	}
	f.Detail = []string{
		fmt.Sprintf("limit entries: %d; judged on a full hourly session: %d; thin session (under %d hourly bars): %d; no hourly bars (daily bar alone): %d", total, full, thinSessionBars, thin, none),
		"hourly bars come from " + appenv.BarDB(a.in.MarketDB, "1h") + "; the daily session bars are derived from them (the session open and range include the 9:00 to 9:30 pre-market half hour)",
	}
	switch {
	case total > 0 && none > 0:
		f.Level = Fail
		f.Summary = fmt.Sprintf("%d of %d limit entries (%s) have no hourly bars and were judged on the daily bar alone", none, total, pct(none, total))
	case total > 0 && float64(thin)/float64(total) > 0.05:
		f.Level = Warn
		f.Summary = fmt.Sprintf("%d of %d limit entries (%s) rest on a thin hourly session", thin, total, pct(thin, total))
	default:
		f.Level = Pass
		f.Summary = fmt.Sprintf("all %d limit entries were judged on hourly bars", total)
	}
	return f
}

// --- 3. LOOKAHEAD --------------------------------------------------------

func (a *assessor) lookahead() Finding {
	f := Finding{Code: "LOOKAHEAD", Title: "Look-ahead symptoms"}
	if a.loadErr != "" {
		return unchecked(f, a.loadErr)
	}
	var checked, entryOut, exitOut int
	for _, t := range a.in.Trades {
		eb, ok1 := a.daily[t.Symbol][t.EntryDate]
		xb, ok2 := a.daily[t.Symbol][t.ExitDate]
		if ok1 {
			checked++
			if outside(t.EntryPrice, eb) {
				entryOut++
			}
		}
		if ok2 && t.ExitReason != models.ExitReasonEndBacktest && outside(t.ExitPrice, xb) {
			exitOut++
		}
	}
	f.Detail = []string{fmt.Sprintf("trades whose entry price is outside that session's daily range: %d of %d; exit price outside the exit session's range: %d", entryOut, checked, exitOut)}

	var leaks []string
	if id := strings.ToLower(a.in.Strategy.ID()); strings.HasSuffix(id, "_tree") && a.in.HoldoutMonths > 0 && a.in.Phase != "" {
		if cutoff, ls, err := train.CheckLeaks(train.CheckConfig{MarketDB: a.in.MarketDB, Table: a.in.Table, HoldoutMonths: a.in.HoldoutMonths}); err == nil {
			need := map[string]bool{}
			if rp, ok := a.in.Strategy.(strategy.RequiredSymbolsProvider); ok {
				for _, s := range rp.RequiredSymbols() {
					need[s] = true
				}
			}
			for _, l := range ls {
				if need[l.Symbol] {
					leaks = append(leaks, fmt.Sprintf("%s tree trained through %s, after the %s cutoff", l.Symbol, l.TrainedThrough, cutoff))
				}
			}
		}
	}
	f.Detail = append(f.Detail, leaks...)
	f.Detail = append(f.Detail, "not checked: look-ahead inside a strategy's own signal SQL (compare signals with a run that is cut off earlier to test that)")
	switch {
	case entryOut+exitOut > 0:
		f.Level = Fail
		f.Summary = fmt.Sprintf("%d fills are priced outside the range the market traded that session: the price came from data the order could not have had", entryOut+exitOut)
	case len(leaks) > 0:
		f.Level = Fail
		f.Summary = fmt.Sprintf("%d trained model(s) saw bars after the holdout cutoff, so the out-of-sample result is not out of sample", len(leaks))
	default:
		f.Level = Pass
		f.Summary = "no fills outside the session's range and no trained model past the cutoff (signal SQL is not audited)"
	}
	return f
}

func outside(price float64, b models.Bar) bool {
	if price <= 0 || b.High <= 0 {
		return false
	}
	return price < b.Low*(1-priceTolerance) || price > b.High*(1+priceTolerance)
}

// --- 4. FILLMODEL --------------------------------------------------------

func (a *assessor) fillModel() Finding {
	f := Finding{Code: "FILLMODEL", Title: "Fill model"}
	if !a.limit {
		f.Level, f.Summary = Pass, "no limit entries; fills are at the bar open or close"
		return f
	}
	var limitFills, touch int
	var touchPnL, totalPnL float64
	for _, t := range a.in.Trades {
		if t.OrderType != "limit" {
			continue
		}
		limitFills++
		totalPnL += t.NetPnL
		sess := a.hourly[t.Symbol][t.EntryDate]
		if len(sess) == 0 {
			continue
		}
		low := sess[0].Low
		for _, b := range sess {
			low = min(low, b.Low)
		}
		if low >= t.EntryPrice*(1-touchOnlyBand) {
			touch++
			touchPnL += t.NetPnL
		}
	}
	f.Detail = []string{
		fmt.Sprintf("limit fills: %d; fills where the session low came within %.1f%% of the fill price (the limit was barely touched): %d, net P&L $%.0f of $%.0f", limitFills, touchOnlyBand*100, touch, touchPnL, totalPnL),
		fmt.Sprintf("assumed: a touch fills the whole order at the limit; no queue position, no partial fills; slippage %.3f%%, commission $%.4f per share", a.in.Config.SlippagePct*100, a.in.Config.CommissionPerShare),
	}
	if limitFills > 0 && float64(touch)/float64(limitFills) >= touchOnlyWarnFrac {
		f.Level = Warn
		f.Summary = fmt.Sprintf("%d of %d limit fills (%s) happened on a bare touch of the limit; a resting order often does not fill there", touch, limitFills, pct(touch, limitFills))
	} else {
		f.Level = Info
		f.Summary = fmt.Sprintf("every limit fill assumes a touch is a fill; %d of %d (%s) were a bare touch", touch, limitFills, pct(touch, limitFills))
	}
	return f
}

// --- 5. DATAGAPS ---------------------------------------------------------

func (a *assessor) dataGaps() Finding {
	f := Finding{Code: "DATAGAPS", Title: "Data gaps"}
	syms := append([]string(nil), a.symbols...)
	if rp, ok := a.in.Strategy.(strategy.RequiredSymbolsProvider); ok && len(rp.RequiredSymbols()) > 0 {
		syms = rp.RequiredSymbols()
	}
	if len(syms) == 0 {
		return unchecked(f, "no symbols to check")
	}
	opts := barcoverage.CoverageOptions{
		Symbols: syms, DB: appenv.BarDB(a.in.MarketDB, "1h"),
		Start: a.in.Report.StartDate, End: a.in.Report.EndDate,
	}
	rows, err := barcoverage.Coverage(opts)
	if err != nil {
		return unchecked(f, err.Error())
	}
	level := Pass
	var bad []string
	minPct, expected, have := 100.0, 0, 0
	for _, c := range rows {
		expected += c.Expected
		have += c.Have
		if c.Expected == 0 {
			continue
		}
		minPct = min(minPct, c.Pct())
		if c.Pct() < sessionGapWarn*100 {
			bad = append(bad, fmt.Sprintf("%s %.1f%% (%d missing, longest gap %d, %.1f bars a day)", c.Symbol, c.Pct(), c.Missing, c.LongestGap, c.BarsPerDay))
		}
	}
	f.Detail = append(f.Detail, fmt.Sprintf("hourly bars: %d of %d sessions across %d symbols (lowest symbol %.1f%%)", have, expected, len(syms), minPct))
	for i, s := range bad {
		if i == 8 {
			f.Detail = append(f.Detail, fmt.Sprintf("  … %d more symbols below %.0f%%", len(bad)-8, sessionGapWarn*100))
			break
		}
		f.Detail = append(f.Detail, "  "+s)
	}
	if len(bad) > 0 {
		level = Warn
	}
	if minPct < sessionGapFail*100 {
		level = Fail
	}
	f.Level = level
	switch level {
	case Pass:
		f.Summary = fmt.Sprintf("every symbol has bars on at least %.0f%% of the sessions the set traded", sessionGapWarn*100)
	case Warn:
		f.Summary = "some symbols are missing sessions; see the list (a thin name can have a day with no trade at all)"
	default:
		f.Summary = fmt.Sprintf("a symbol has bars on less than %.0f%% of the sessions; signals and fills around the gaps are missing", sessionGapFail*100)
	}
	return f
}

// --- 6. OVERFIT ----------------------------------------------------------

func (a *assessor) overfit() Finding {
	f := Finding{Code: "OVERFIT", Title: "Overfitting and selection"}
	level := Pass
	raise := func(l Level) { level = max(level, l) }
	var notes []string

	if a.in.Phase == "" || a.in.HoldoutMonths <= 0 {
		raise(Warn)
		notes = append(notes, "no held-out period: every number here is in-sample")
	}
	if n, runs := sweptConfigs(a.in.Strategy.ID()); n > 0 {
		raise(Info)
		notes = append(notes, fmt.Sprintf("this strategy was swept %d times across %d gridsearch run(s); its parameters are the best of those, so in-sample results are biased upward", n, runs))
	}
	switch n := a.in.Report.TotalTrades; {
	case n < smallSample:
		raise(Warn)
		notes = append(notes, fmt.Sprintf("only %d trades: too few to separate skill from luck", n))
	}
	if p := a.in.Prior; p != nil {
		cur := a.in.Report
		notes = append(notes, fmt.Sprintf("in-sample → out-of-sample: CAGR %.1f%% → %.1f%%, win rate %.1f%% → %.1f%%, profit factor %.2f → %.2f",
			p.CAGR*100, cur.CAGR*100, p.WinRate*100, cur.WinRate*100, p.ProfitFactor, cur.ProfitFactor))
		switch {
		case p.CAGR > 0 && cur.CAGR < p.CAGR*0.5, p.ProfitFactor > 1 && cur.ProfitFactor < 1, p.WinRate-cur.WinRate > 0.10:
			raise(Fail)
			notes = append(notes, "the held-out result is less than half the in-sample one, or loses money, or the win rate fell more than 10 points: overfitting is likely")
		}
	}
	f.Detail = notes
	f.Level = level
	switch level {
	case Pass:
		f.Summary = "held-out period in use, enough trades, no sweep on record"
	case Info:
		f.Summary = "parameters were selected on this data; judge on the held-out period"
	case Warn:
		f.Summary = "no held-out check or too few trades"
	default:
		f.Summary = "the out-of-sample result collapsed against the in-sample one"
	}
	return f
}

// sweptConfigs counts the gridsearch results recorded for a strategy in the
// run folders under the reports directory, and the runs they came from.
func sweptConfigs(id string) (configs, runs int) {
	dbs, _ := filepath.Glob(filepath.Join(appenv.Reports(), "*", "gridsearch.db"))
	for _, p := range dbs {
		db, err := sqlx.Open("sqlite", "file:"+p+"?mode=ro")
		if err != nil {
			continue
		}
		var n int
		if err := db.Get(&n, `SELECT COUNT(*) FROM gridsearch_results WHERE strategy_id = ?`, id); err == nil && n > 0 {
			configs += n
			runs++
		}
		db.Close()
	}
	return
}

func unchecked(f Finding, why string) Finding {
	f.Level = Warn
	f.Summary = "could not be checked: " + why
	return f
}

// Verdict counts the findings by level.
func Verdict(fs []Finding) (fail, warn int) {
	for _, f := range fs {
		switch f.Level {
		case Fail:
			fail++
		case Warn:
			warn++
		}
	}
	return
}

// Print writes the reliability block.
func Print(w io.Writer, title string, fs []Finding) {
	fail, warn := Verdict(fs)
	fmt.Fprintf(w, "\n========================================================================================\n")
	fmt.Fprintf(w, "🔎 BACKTEST RELIABILITY %s\n", title)
	fmt.Fprintf(w, "========================================================================================\n")
	for _, f := range fs {
		fmt.Fprintf(w, "[%-4s] %-10s %s: %s\n", f.Level, f.Code, f.Title, f.Summary)
		if f.Level >= Info {
			for _, d := range f.Detail {
				fmt.Fprintf(w, "         · %s\n", d)
			}
		}
	}
	switch {
	case fail > 0:
		fmt.Fprintf(w, "⛔ %d FAIL, %d WARN: do not rely on the numbers below until the failures are fixed.\n", fail, warn)
	case warn > 0:
		fmt.Fprintf(w, "⚠️  %d WARN: read the numbers below with these caveats.\n", warn)
	default:
		fmt.Fprintf(w, "✅ no reliability problems detected (see the limits of each check above).\n")
	}
	fmt.Fprintf(w, "========================================================================================\n")
}

// Groups turns the findings into a report parameter group.
func Groups(fs []Finding) []models.ParamGroup {
	g := &models.ParamGroup{Title: "Backtest reliability"}
	for _, f := range fs {
		g.Add(f.Code+" – "+f.Title, f.Level.String()+": "+f.Summary)
		for _, d := range f.Detail {
			g.Add("", d)
		}
	}
	return []models.ParamGroup{*g}
}

// PrintCompact writes one line for a strategy: the code of each check that is
// a WARN or FAIL, or that nothing was found.
func PrintCompact(w io.Writer, id string, fs []Finding) {
	var parts []string
	for _, f := range fs {
		if f.Level >= Warn {
			parts = append(parts, f.Level.String()+" "+f.Code)
		}
	}
	if len(parts) == 0 {
		parts = []string{"no problems detected"}
	}
	fmt.Fprintf(w, "  %-60s %s\n", id, strings.Join(parts, ", "))
}
