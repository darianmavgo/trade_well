package sim

import (
	"fmt"
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// ohlc is a bar's open, high, low, close.
type ohlc [4]float64

// session builds the 7 regular hours (09:00 to 15:00 ET) of a day, flat at px,
// with the listed hours (0 = 09:00 ... 6 = 15:00) replaced.
func session(date string, px float64, over map[int]ohlc) []Bar {
	var out []Bar
	for h := 0; h < 7; h++ {
		o := ohlc{px, px, px, px}
		if v, ok := over[h]; ok {
			o = v
		}
		out = append(out, Bar{T: fmt.Sprintf("%s %02d:00", date, 9+h), Open: o[0], High: o[1], Low: o[2], Close: o[3]})
	}
	return out
}

func series(days ...[]Bar) []Bar {
	var out []Bar
	for _, d := range days {
		out = append(out, d...)
	}
	return out
}

func calendar(bs ...[]Bar) []string {
	seen := map[string]bool{}
	var out []string
	for _, b := range bs {
		for _, x := range b {
			if !seen[x.Session()] {
				seen[x.Session()] = true
				out = append(out, x.Session())
			}
		}
	}
	return out
}

func one(bs []Bar, sigs []Signal, cfg Config) *Result {
	return Run(sigs, map[string][]Bar{"A": bs}, calendar(bs), cfg)
}

func sig(date string, close float64) Signal {
	return Signal{Symbol: "A", Date: date, Entry: 1, Close: close}
}

func TestDefaultEntryBuysTheSignalSessionsCloseInWholeSharesAt20Percent(t *testing.T) {
	bs := series(session("2025-01-02", 100, map[int]ohlc{6: {100, 101, 99, 100.5}}), session("2025-01-03", 100, nil))
	r := one(bs, []Signal{sig("2025-01-02", 100.5)}, Config{PositionCap: 1})
	tr := r.Trades[0]
	// 20% of 100,000 at the 15:00 close 100.5 = 199 shares
	if tr.Shares != 199 || !near(tr.EntryPrice, 100.5) || tr.Entry != "2025-01-02 15:00" {
		t.Fatalf("got %+v", tr)
	}
}

func TestProfitTargetFillsInTheHourItTradesAndGapsFillAtTheOpen(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil),
		session("2025-01-03", 100, map[int]ohlc{2: {100, 106, 100, 105}, 3: {108, 109, 107, 108}}))
	cfg := Config{PositionCap: 1, TakeProfitPct: 0.05, TargetPct: 1.05}
	r := one(bs, []Signal{sig("2025-01-02", 100)}, cfg)
	tr := r.Trades[0]
	if tr.Reason != "profit_target" || !near(tr.ExitPrice, 105) || tr.Exit != "2025-01-03 11:00" {
		t.Fatalf("target should fill at 105 in the 11:00 hour, got %+v", tr)
	}
	// a hour that opens through the target fills at its open
	bs = series(session("2025-01-02", 100, nil), session("2025-01-03", 100, map[int]ohlc{2: {110, 112, 109, 111}}))
	r = one(bs, []Signal{sig("2025-01-02", 100)}, cfg)
	if tr := r.Trades[0]; !near(tr.ExitPrice, 110) {
		t.Fatalf("gap should fill at the open 110, got %+v", tr)
	}
}

func TestStopWinsAnHourThatSpansBoth(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil), session("2025-01-03", 100, map[int]ohlc{1: {100, 120, 80, 100}}))
	r := one(bs, []Signal{sig("2025-01-02", 100)}, Config{PositionCap: 1, TargetPct: 1.05, StopLossPct: 0.95})
	if r.Trades[0].Reason != "stop_loss" {
		t.Fatalf("got %+v", r.Trades[0])
	}
}

func TestHoldWindowSellsAtTheSessionCloseAndSlippageCosts(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil), session("2025-01-03", 100, nil),
		session("2025-01-06", 100, map[int]ohlc{6: {100, 110, 100, 110}}), session("2025-01-07", 100, nil))
	r := one(bs, []Signal{sig("2025-01-02", 100)}, Config{PositionCap: 1, HoldingWindow: 2, Slippage: 0.01})
	tr := r.Trades[0]
	if tr.Reason != "time_up" || tr.Exit != "2025-01-06 15:00" || tr.HoldDays != 2 || !near(tr.ExitPrice, 110*0.99) || !near(tr.EntryPrice, 101) {
		t.Fatalf("got %+v", tr)
	}
}

func TestNextDayOpenEntryBuysTheFirstHoursOpenAndCrisisStopApplies(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil),
		session("2025-01-03", 105, map[int]ohlc{0: {105, 106, 104, 105}, 3: {105, 105, 80, 82}}))
	cfg := Config{PositionCap: 1, NextDayOpenEntry: true, CrisisStop: 0.20}
	r := one(bs, []Signal{sig("2025-01-02", 100)}, cfg)
	tr := r.Trades[0]
	// filled at 105 at 09:00; no stop on the row, so the crisis stop 84 trips at 12:00
	if tr.Entry != "2025-01-03 09:00" || !near(tr.EntryPrice, 105) || tr.Reason != "stop_loss" || !near(tr.ExitPrice, 84) {
		t.Fatalf("got %+v", tr)
	}
}

// The case that made the hourly bars necessary: a 95% limit fills at 11:00 after
// the day's high came at 09:00. The 3% target must not be taken for that high.
func TestALimitFillCannotTakeATargetThatPrecededIt(t *testing.T) {
	prev := session("2025-01-02", 100, nil)
	day := session("2025-01-03", 95, map[int]ohlc{ // hours after the fill sit at 95, under the 99.91 target
		0: {102, 104, 101, 103}, // the high of the day: above the target, before any fill
		1: {103, 103, 98, 99},   // low 98 stays above the 97 limit
		2: {99, 99, 94, 95},     // 11:00: trades down to the limit
	})
	cfg := Config{PositionCap: 1, EntryLimitPct: 0.97, EntrySameDay: true, TakeProfitPct: 0.03, HoldingWindow: 5}
	sg := []Signal{{Symbol: "A", Date: "2025-01-03", Entry: 1, Close: 100}}
	r := one(series(prev, day, session("2025-01-06", 95, nil)), sg, cfg)
	tr := r.Trades[0]
	if tr.Entry != "2025-01-03 11:00" || !near(tr.EntryPrice, 97) {
		t.Fatalf("limit should fill at 97 in the 11:00 hour, got %+v", tr)
	}
	if tr.Reason == "profit_target" {
		t.Fatalf("took a target the 09:00 high touched before the fill: %+v", tr)
	}

	// the same target is taken when a LATER hour reaches it
	day2 := session("2025-01-03", 95, map[int]ohlc{
		0: {102, 104, 101, 103}, 1: {103, 103, 98, 99}, 2: {99, 99, 94, 95},
		4: {96, 101, 96, 100}, // 13:00 reaches 99.91
	})
	r = one(series(prev, day2, session("2025-01-06", 95, nil)), sg, cfg)
	tr = r.Trades[0]
	if tr.Reason != "profit_target" || tr.Exit != "2025-01-03 13:00" {
		t.Fatalf("a later hour reaching the target should take it that day, got %+v", tr)
	}
}

func TestALimitHourThatOpensUnderTheLimitFillsAtTheOpen(t *testing.T) {
	prev := session("2025-01-02", 100, nil)
	day := session("2025-01-03", 100, map[int]ohlc{1: {96, 99, 95, 98}})
	r := one(series(prev, day, session("2025-01-06", 100, nil)), []Signal{{Symbol: "A", Date: "2025-01-03", Entry: 1, Close: 100}},
		Config{PositionCap: 1, EntryLimitPct: 0.97, EntrySameDay: true, HoldingWindow: 1})
	if tr := r.Trades[0]; tr.Entry != "2025-01-03 10:00" || !near(tr.EntryPrice, 96) {
		t.Fatalf("got %+v", tr)
	}
}

func TestALimitThatNeverTradesDownDoesNotFill(t *testing.T) {
	prev := session("2025-01-02", 100, nil)
	day := session("2025-01-03", 100, map[int]ohlc{2: {100, 101, 98, 100}}) // low 98 > 97
	r := one(series(prev, day), []Signal{{Symbol: "A", Date: "2025-01-03", Entry: 1, Close: 100}}, Config{PositionCap: 1, EntryLimitPct: 0.97, EntrySameDay: true})
	if len(r.Trades) != 0 {
		t.Fatalf("got %+v", r.Trades)
	}
}

func TestOneFillPerMonthAndCashLimitedBookIsDeterministic(t *testing.T) {
	names := []string{"A", "B", "C", "D", "E", "F"}
	bars := map[string][]Bar{}
	var cal []string
	for _, s := range names {
		bars[s] = series(session("2025-01-02", 100, nil), session("2025-01-03", 100, nil), session("2025-01-06", 100, nil), session("2025-01-07", 100, nil))
		cal = calendar(bars[s])
	}
	var sigs []Signal
	for _, s := range names {
		sigs = append(sigs, Signal{Symbol: s, Date: "2025-01-02", Entry: 1, Close: 100}, Signal{Symbol: s, Date: "2025-01-06", Entry: 1, Close: 100})
	}
	cfg := Config{PositionCap: 6, Allocation: 0.2, HoldingWindow: 1, OneFillPerMonth: true}
	for i := 0; i < 100; i++ {
		r := Run(sigs, bars, cal, cfg)
		// Jan 2 close: five 20% positions take all the cash (alphabetical); F is skipped and
		// not marked filled. Jan 6: A-E are on their monthly cooldown, so F fills then.
		if len(r.Trades) != 6 {
			t.Fatalf("run %d: %d trades, want 6", i, len(r.Trades))
		}
		for j, tr := range r.Trades {
			day := "2025-01-02"
			if j == 5 {
				day = "2025-01-06"
			}
			if tr.Symbol != names[j] || tr.Entry[:10] != day {
				t.Fatalf("run %d: trade %d is %s on %s, want %s on %s", i, j, tr.Symbol, tr.Entry, names[j], day)
			}
		}
	}
}

func TestExitSignalSellsAtTheSessionClose(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil), session("2025-01-03", 100, map[int]ohlc{6: {100, 107, 100, 107}}), session("2025-01-06", 100, nil))
	r := one(bs, []Signal{sig("2025-01-02", 100), {Symbol: "A", Date: "2025-01-03", Entry: -1, Close: 107}}, Config{PositionCap: 1})
	tr := r.Trades[0]
	if tr.Reason != "signal" || tr.Exit != "2025-01-03 15:00" || !near(tr.ExitPrice, 107) {
		t.Fatalf("got %+v", tr)
	}
}

func TestShortPnLAndCollateral(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil), session("2025-01-03", 100, map[int]ohlc{6: {100, 100, 90, 90}}))
	s := Signal{Symbol: "A", Date: "2025-01-02", Entry: 1, Short: true, Close: 100}
	x := Signal{Symbol: "A", Date: "2025-01-03", Entry: -1, Close: 90}
	r := one(bs, []Signal{s, x}, Config{PositionCap: 1, Allocation: 0.5})
	tr := r.Trades[0]
	// 500 shares short from 100 covered at 90: +10%, +5,000
	if tr.Direction != "SHORT" || tr.Shares != 500 || !near(tr.ReturnPct, 0.10) || !near(tr.NetPnL, 5000) {
		t.Fatalf("got %+v", tr)
	}
	if got := r.Curve[len(r.Curve)-1].Equity; !near(got, 105000) {
		t.Fatalf("final equity %v, want 105000", got)
	}
}

// A signal known at one session's close, entered the next session: the limit is a share
// of the signal session's close (the previous close of the entry session).
func TestEntryLimitPctOnANextSessionFamilyRestsAtAShareOfTheSignalSessionsClose(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil),
		session("2025-01-03", 100, map[int]ohlc{2: {100, 100, 96, 97}}), session("2025-01-06", 100, nil))
	r := one(bs, []Signal{sig("2025-01-02", 100)}, Config{PositionCap: 1, EntryLimitPct: 0.97, HoldingWindow: 1})
	if len(r.Trades) != 1 || !near(r.Trades[0].EntryPrice, 97) || r.Trades[0].Entry[:10] != "2025-01-03" {
		t.Fatalf("trades = %+v, want a fill at 97 on 01-03", r.Trades)
	}
	// 1.0 is the previous close itself: the first hour that trades down to it
	r = one(bs, []Signal{sig("2025-01-02", 100)}, Config{PositionCap: 1, EntryLimitPct: 1.0, HoldingWindow: 1})
	if len(r.Trades) != 1 || !near(r.Trades[0].EntryPrice, 100) || r.Trades[0].Entry != "2025-01-03 09:00" {
		t.Fatalf("1.0 trades = %+v, want a fill at 100 in the first hour of 01-03", r.Trades)
	}
}

func TestNegativeEntryLimitPctBuysTheEntrySessionsOpen(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil),
		session("2025-01-03", 105, map[int]ohlc{0: {103, 106, 102, 105}}), session("2025-01-06", 105, nil))
	r := one(bs, []Signal{sig("2025-01-02", 100)}, Config{PositionCap: 1, EntryLimitPct: -1, HoldingWindow: 1})
	if len(r.Trades) != 1 || !near(r.Trades[0].EntryPrice, 103) || r.Trades[0].Entry != "2025-01-03 09:00" {
		t.Fatalf("trades = %+v, want a fill at the 01-03 open, 103, whatever the price", r.Trades)
	}
}

func withVolume(bs []Bar, v float64) []Bar {
	for i := range bs {
		bs[i].Volume = v
	}
	return bs
}

func TestMaxBarVolumeShareCutsAnEntryToWhatTheHourTraded(t *testing.T) {
	mk := func() []Bar {
		return series(session("2025-01-02", 100, map[int]ohlc{6: {100, 101, 99, 100.5}}), session("2025-01-03", 100, nil))
	}
	// 20% of 100000 buys 199 shares at 100.5; the last hour traded 1000 shares, 5% of it is 50.
	r := one(withVolume(mk(), 1000), []Signal{sig("2025-01-02", 100.5)}, Config{PositionCap: 1, MaxBarVolumeShare: 0.05})
	if len(r.Trades) != 1 || r.Trades[0].Shares != 50 {
		t.Fatalf("trades %+v; want one of 50 shares (5%% of 1000)", r.Trades)
	}
	// no cap set: the whole 199
	r = one(withVolume(mk(), 1000), []Signal{sig("2025-01-02", 100.5)}, Config{PositionCap: 1})
	if len(r.Trades) != 1 || r.Trades[0].Shares != 199 {
		t.Fatalf("uncapped trades %+v; want 199 shares", r.Trades)
	}
	// an hour with no volume cannot fill at all
	r = one(withVolume(mk(), 0), []Signal{sig("2025-01-02", 100.5)}, Config{PositionCap: 1, MaxBarVolumeShare: 0.05})
	if len(r.Trades) != 0 {
		t.Fatalf("trades %+v; want none in an hour without volume", r.Trades)
	}
}

// two symbols, flat prices, for the shared-account tests
func twoSymbols(days ...string) map[string][]Bar {
	var a, b []Bar
	for _, d := range days {
		a = append(a, session(d, 100, nil)...)
		b = append(b, session(d, 50, nil)...)
	}
	return map[string][]Bar{"A": a, "B": b}
}

func sessionsOf(bars map[string][]Bar) []string {
	var all [][]Bar
	for _, bs := range bars {
		all = append(all, bs)
	}
	return calendar(all...)
}

func sigOn(sym, date string, close float64) Signal {
	return Signal{Symbol: sym, Date: date, Entry: 1, Close: close}
}

func TestPortfolioSleevesShareOneCashBalance(t *testing.T) {
	bars := twoSymbols("2025-01-02", "2025-01-03")
	// each sleeve wants 60% of the account: the primary gets 600 shares worth, the subordinate only what is left
	cfg := Config{Allocation: 0.6, PositionCap: 1}
	r := RunPortfolio([]Sleeve{
		{ID: "p", Priority: 0, Cfg: cfg, Sigs: []Signal{sigOn("A", "2025-01-02", 100)}},
		{ID: "s", Priority: 1, Cfg: cfg, Sigs: []Signal{sigOn("B", "2025-01-02", 50)}},
	}, bars, sessionsOf(bars))
	got := map[string]int{}
	for _, tr := range r.Trades {
		got[tr.Strategy] = tr.Shares
	}
	if got["p"] != 600 {
		t.Errorf("primary shares = %d, want 600 (60%% of 100000 at 100)", got["p"])
	}
	// 40000 left, 95% of it at 50 = 760 shares
	if got["s"] != 760 {
		t.Errorf("subordinate shares = %d, want 760 (95%% of the 40000 left at 50)", got["s"])
	}
}

func TestPortfolioPrimaryPreemptsASubordinateForCash(t *testing.T) {
	bars := twoSymbols("2025-01-02", "2025-01-03", "2025-01-06")
	cfg := Config{Allocation: 0.9, PositionCap: 1}
	r := RunPortfolio([]Sleeve{
		{ID: "p", Priority: 0, Cfg: cfg, Sigs: []Signal{sigOn("A", "2025-01-03", 100)}},
		{ID: "s", Priority: 1, Cfg: cfg, Sigs: []Signal{sigOn("B", "2025-01-02", 50)}},
	}, bars, sessionsOf(bars))
	var sub, prim *Trade
	for i := range r.Trades {
		switch r.Trades[i].Strategy {
		case "s":
			sub = &r.Trades[i]
		case "p":
			prim = &r.Trades[i]
		}
	}
	if sub == nil || sub.Reason != "preempted" {
		t.Fatalf("subordinate trade %+v; want it sold as preempted", sub)
	}
	if prim == nil || prim.Shares != 900 {
		t.Fatalf("primary trade %+v; want 900 shares (90%% of the whole account)", prim)
	}
}

func TestPortfolioASubordinateCannotTakeASymbolTheOtherHolds(t *testing.T) {
	bars := twoSymbols("2025-01-02", "2025-01-03")
	cfg := Config{PositionCap: 1}
	r := RunPortfolio([]Sleeve{
		{ID: "p", Priority: 0, Cfg: cfg, Sigs: []Signal{sigOn("A", "2025-01-02", 100)}},
		{ID: "s", Priority: 1, Cfg: cfg, Sigs: []Signal{sigOn("A", "2025-01-02", 100)}},
	}, bars, sessionsOf(bars))
	if len(r.Trades) != 1 || r.Trades[0].Strategy != "p" {
		t.Fatalf("trades %+v; want only the primary's", r.Trades)
	}
}

func TestPortfolioExitSignalSellsOnlyItsOwnSleevesPosition(t *testing.T) {
	bars := twoSymbols("2025-01-02", "2025-01-03", "2025-01-06")
	cfg := Config{PositionCap: 2}
	exit := Signal{Symbol: "B", Date: "2025-01-03", Entry: -1, Close: 50}
	r := RunPortfolio([]Sleeve{
		{ID: "p", Priority: 0, Cfg: cfg, Sigs: []Signal{sigOn("A", "2025-01-02", 100)}},
		{ID: "s", Priority: 1, Cfg: cfg, Sigs: []Signal{sigOn("B", "2025-01-02", 50), exit}},
	}, bars, sessionsOf(bars))
	reasons := map[string]string{}
	for _, tr := range r.Trades {
		reasons[tr.Strategy] = tr.Reason
	}
	if reasons["s"] != "signal" || reasons["p"] != "end" {
		t.Errorf("exit reasons %v; want the subordinate sold on its signal and the primary held to the end", reasons)
	}
}

// No exit triggers in the hour a position was entered in: a stop, a target or a trailing stop is
// first judged on the next hour bar. A wide entry bar that touches both levels changes nothing.
func TestNoExitTriggersInTheEntryHour(t *testing.T) {
	// a 97 limit fills in the 11:00 bar, whose range (90 to 110) touches both the 92 stop and the 105 target
	prev := session("2025-01-02", 100, nil)
	day := session("2025-01-03", 99, map[int]ohlc{2: {99, 110, 90, 95}})
	next := session("2025-01-06", 99, nil)
	cfg := Config{PositionCap: 1, EntryLimitPct: 0.97, EntrySameDay: true, HoldingWindow: 1}
	r := one(series(prev, day, next), []Signal{{Symbol: "A", Date: "2025-01-03", Entry: 1, Close: 100, TakeProfit: 105, StopLoss: 92}}, cfg)
	if len(r.Trades) != 1 {
		t.Fatalf("trades %+v, want one", r.Trades)
	}
	tr := r.Trades[0]
	if tr.Entry != "2025-01-03 11:00" || tr.Exit == tr.Entry || tr.Reason != "time_up" || tr.Exit != "2025-01-06 15:00" {
		t.Fatalf("got %+v, want entry at 11:00 and the hold-window exit on the next session", tr)
	}
}

// The same wide bar, one hour later, does trigger the level: the next bar is the first that can.
func TestTheNextHourBarIsTheFirstThatCanStopOrTakeProfit(t *testing.T) {
	prev := session("2025-01-02", 100, nil)
	for _, c := range []struct {
		name   string
		bar    ohlc
		reason string
		price  float64
	}{
		{"stop", ohlc{95, 96, 90, 93}, "stop_loss", 92},
		{"target", ohlc{95, 106, 94, 105}, "profit_target", 105},
	} {
		t.Run(c.name, func(t *testing.T) {
			day := session("2025-01-03", 99, map[int]ohlc{2: {99, 110, 90, 95}, 3: c.bar})
			r := one(series(prev, day), []Signal{{Symbol: "A", Date: "2025-01-03", Entry: 1, Close: 100, TakeProfit: 105, StopLoss: 92}},
				Config{PositionCap: 1, EntryLimitPct: 0.97, EntrySameDay: true})
			if len(r.Trades) != 1 {
				t.Fatalf("trades %+v, want one", r.Trades)
			}
			tr := r.Trades[0]
			if tr.Entry != "2025-01-03 11:00" || tr.Exit != "2025-01-03 12:00" || tr.Reason != c.reason || !near(tr.ExitPrice, c.price) {
				t.Fatalf("got %+v, want %s at %v in the 12:00 bar", tr, c.reason, c.price)
			}
		})
	}
}

// A next-day-open entry is the same: its first bar may span the stop, and the exit comes no earlier
// than the second bar.
func TestAnOpenEntryIsNotStoppedOnItsOwnBar(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil),
		session("2025-01-03", 105, map[int]ohlc{0: {105, 130, 70, 100}, 1: {100, 100, 100, 100}}))
	r := one(bs, []Signal{sig("2025-01-02", 100)}, Config{PositionCap: 1, NextDayOpenEntry: true, CrisisStop: 0.20, HoldingWindow: 1})
	tr := r.Trades[0]
	if tr.Entry != "2025-01-03 09:00" || tr.Exit <= tr.Entry {
		t.Fatalf("got %+v, want an exit after the 09:00 entry bar", tr)
	}
}

// A long whose stop is at or above its fill would exit on the next bar at a price it never traded at,
// a gain nobody could book. The order is not filled.
func TestALongWithItsStopAtOrAboveTheFillIsNotEntered(t *testing.T) {
	prev := session("2025-01-02", 100, nil)
	day := session("2025-01-03", 99, map[int]ohlc{2: {99, 99, 94, 95}}) // the 97 limit fills at 11:00
	cfg := Config{PositionCap: 1, EntryLimitPct: 0.97, EntrySameDay: true, HoldingWindow: 1}
	for _, c := range []struct {
		stop   float64
		trades int
	}{{98, 0}, {97, 0}, {96.9, 1}, {90, 1}} {
		r := one(series(prev, day, session("2025-01-06", 99, nil)), []Signal{{Symbol: "A", Date: "2025-01-03", Entry: 1, Close: 100, StopLoss: c.stop}}, cfg)
		if len(r.Trades) != c.trades {
			t.Errorf("stop %v: %d trades %+v, want %d", c.stop, len(r.Trades), r.Trades, c.trades)
		}
	}
}

// A position opened in the final hour has no later bar to exit on: it is still open, not a trade
// that entered and exited in one hour.
func TestAPositionOpenedInTheFinalHourIsNotATrade(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil), session("2025-01-03", 100, nil))
	r := one(bs, []Signal{sig("2025-01-03", 100)}, Config{PositionCap: 1})
	if len(r.Trades) != 0 {
		t.Fatalf("trades %+v, want none", r.Trades)
	}
	if last := r.Curve[len(r.Curve)-1]; last.OpenPositions != 1 {
		t.Fatalf("last point %+v, want the position still open", last)
	}
}

// No trade of any run enters and exits in the same hour, whatever the bars do.
func TestNoTradeEverEntersAndExitsInOneHour(t *testing.T) {
	wild := map[int]ohlc{0: {100, 130, 70, 100}, 1: {100, 130, 70, 100}, 2: {100, 130, 70, 100}, 3: {100, 130, 70, 100}, 4: {100, 130, 70, 100}, 5: {100, 130, 70, 100}, 6: {100, 130, 70, 100}}
	bs := series(session("2025-01-02", 100, wild), session("2025-01-03", 100, wild), session("2025-01-06", 100, wild))
	for _, cfg := range []Config{
		{PositionCap: 1, NextDayOpenEntry: true, CrisisStop: 0.05, TargetPct: 1.02, HoldingWindow: 1},
		{PositionCap: 1, EntryLimitPct: 0.97, EntrySameDay: true, TakeProfitPct: 0.02, StopLossPct: 0.05, HoldingWindow: 2},
		{PositionCap: 3, TrailingStop: 0.01, HoldingWindow: 1},
	} {
		r := one(bs, []Signal{sig("2025-01-02", 100), sig("2025-01-03", 100)}, cfg)
		for _, tr := range r.Trades {
			if tr.Exit <= tr.Entry {
				t.Errorf("cfg %+v: trade %+v exits in or before its entry hour", cfg, tr)
			}
		}
	}
}

// An order is one entry. After the position it opened exits, the same signal does not buy again.
func TestASignalBuysOnceEvenAfterItsPositionIsStoppedOut(t *testing.T) {
	prev := session("2025-01-02", 100, nil)
	// the 97 limit fills at 11:00; the 12:00 bar stops it out at 92; the later hours trade at 90, under the limit again
	day := session("2025-01-03", 90, map[int]ohlc{0: {99, 99, 99, 99}, 1: {99, 99, 99, 99}, 2: {99, 99, 96, 98}, 3: {98, 98, 90, 91}})
	r := one(series(prev, day), []Signal{{Symbol: "A", Date: "2025-01-03", Entry: 1, Close: 100, StopLoss: 92}},
		Config{PositionCap: 1, EntryLimitPct: 0.97, EntrySameDay: true})
	if len(r.Trades) != 1 || r.Trades[0].Reason != "stop_loss" {
		t.Fatalf("trades %+v, want the one stopped-out trade and no re-entry", r.Trades)
	}
}

func TestFixedSharesBuysThatManyWhateverTheEquity(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil), session("2025-01-03", 100, nil), session("2025-01-06", 100, nil))
	r := one(bs, []Signal{sig("2025-01-02", 100)}, Config{PositionCap: 1, InitialCapital: 1_000_000, FixedShares: 1, HoldingWindow: 1})
	if len(r.Trades) != 1 || r.Trades[0].Shares != 1 {
		t.Fatalf("trades %+v, want one trade of one share", r.Trades)
	}
	r = one(bs, []Signal{sig("2025-01-02", 100)}, Config{PositionCap: 1, InitialCapital: 150, FixedShares: 5, HoldingWindow: 1})
	if len(r.Trades) != 1 || r.Trades[0].Shares != 1 {
		t.Fatalf("trades %+v, want the 5 shares cut to what 95%% of $150 buys", r.Trades)
	}
}

func TestAPositionOpenedInTheFinalHourIsReportedOpen(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil), session("2025-01-03", 100, nil))
	r := one(bs, []Signal{sig("2025-01-03", 100)}, Config{PositionCap: 1})
	if len(r.Trades) != 0 || len(r.Open) != 1 || r.Open[0].Symbol != "A" || r.Open[0].EntryPrice != 100 {
		t.Fatalf("trades %+v open %+v, want no trade and one open position", r.Trades, r.Open)
	}
}

func parkBars() (map[string][]Bar, []string) {
	p := series(session("2025-01-02", 50, nil), session("2025-01-03", 50, nil), session("2025-01-06", 50, nil))
	a := series(session("2025-01-02", 100, nil), session("2025-01-03", 100, nil), session("2025-01-06", 100, nil))
	return map[string][]Bar{"A": a, "P": p}, calendar(a)
}

func TestParkHoldsLeftoverCashInTheDefaultAsset(t *testing.T) {
	bars, days := parkBars()
	r := RunPortfolioWith([]Sleeve{{Cfg: Config{PositionCap: 1, InitialCapital: 100000}}}, bars, days,
		&Park{Symbol: "P", Dividends: map[string]float64{"2025-01-03": 1}})
	if len(r.Trades) != 0 {
		t.Fatalf("the park booked a trade: %+v", r.Trades)
	}
	if r.Park.Symbol != "P" || !near(r.Park.Dividends, 2000) {
		t.Fatalf("park %+v, want symbol P and $2000 of dividends (2000 shares x $1)", r.Park)
	}
	if r.Park.AvgWeight < 0.99 || r.Park.DaysUnparked != 0 {
		t.Errorf("park %+v, want fully invested every session", r.Park)
	}
	for _, pt := range r.Curve {
		if pt.Cash > 100 || pt.OpenPositions != 1 {
			t.Errorf("%s: cash %.2f open %d, want the cash swept into the park lot", pt.Date, pt.Cash, pt.OpenPositions)
		}
	}
}

func TestAnEntrySellsParkSharesForTheCashItNeeds(t *testing.T) {
	bars, days := parkBars()
	cfg := Config{PositionCap: 1, InitialCapital: 100000, Allocation: 0.5}
	r := RunPortfolioWith([]Sleeve{{Cfg: cfg, Sigs: []Signal{sig("2025-01-03", 100)}}}, bars, days, &Park{Symbol: "P"})
	if len(r.Trades) != 1 || r.Trades[0].Shares != 500 {
		t.Fatalf("trades %+v, want one trade of 500 shares (half of $100000 at $100)", r.Trades)
	}
	if got := r.Curve[1]; got.OpenPositions != 2 || got.Equity < 99900 {
		t.Errorf("after the entry %+v, want the trade and the remaining park lot, equity intact", got)
	}
}

func TestNoParkLeavesTheRunUnchanged(t *testing.T) {
	bars, days := parkBars()
	cfg := Config{PositionCap: 1, InitialCapital: 100000, Allocation: 0.5}
	with := RunPortfolioWith([]Sleeve{{Cfg: cfg, Sigs: []Signal{sig("2025-01-03", 100)}}}, bars, days, nil)
	without := RunPortfolio([]Sleeve{{Cfg: cfg, Sigs: []Signal{sig("2025-01-03", 100)}}}, bars, days)
	if len(with.Trades) != len(without.Trades) || with.Park != (ParkResult{}) {
		t.Fatalf("a nil park changed the run: %+v vs %+v", with, without)
	}
}

func TestFixedDollarBuysThatManyDollarsAndIsCutToTheCash(t *testing.T) {
	bs := series(session("2025-01-02", 100, nil), session("2025-01-03", 100, nil), session("2025-01-06", 100, nil))
	r := one(bs, []Signal{sig("2025-01-02", 100)}, Config{PositionCap: 1, InitialCapital: 1_000_000, FixedDollar: 2500, HoldingWindow: 1})
	if len(r.Trades) != 1 || r.Trades[0].Shares != 25 {
		t.Fatalf("trades %+v, want 25 shares ($2500 at $100)", r.Trades)
	}
	r = one(bs, []Signal{sig("2025-01-02", 100)}, Config{PositionCap: 1, InitialCapital: 1000, FixedDollar: 2500, HoldingWindow: 1})
	if len(r.Trades) != 1 || r.Trades[0].Shares != 9 {
		t.Fatalf("trades %+v, want 9 shares (95%% of $1000 at $100)", r.Trades)
	}
}

// A signal's own allocation sizes the position unless the config is flat, in which case every
// position is the config's share of equity.
func TestFlatAllocIgnoresTheSignalsOwnAllocation(t *testing.T) {
	bs := series(session("2025-01-02", 100, map[int]ohlc{6: {100, 101, 99, 100.5}}), session("2025-01-03", 100, nil))
	s := sig("2025-01-02", 100.5)
	s.Alloc = 0.05

	graded := one(bs, []Signal{s}, Config{PositionCap: 1, Allocation: 0.20}).Trades[0]
	// 5% of 100,000 at 100.5 = 49 shares
	if graded.Shares != 49 {
		t.Fatalf("signal allocation should size the position, got %d shares", graded.Shares)
	}
	flat := one(bs, []Signal{s}, Config{PositionCap: 1, Allocation: 0.20, FlatAlloc: true}).Trades[0]
	// 20% of 100,000 at 100.5 = 199 shares
	if flat.Shares != 199 {
		t.Fatalf("flat allocation should ignore the signal's 5%%, got %d shares", flat.Shares)
	}
}
