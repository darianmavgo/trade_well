package hoursim

import (
	"errors"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

func hb(date string, o, h, l, c float64) models.Bar {
	return models.Bar{Date: date, Open: o, High: h, Low: l, Close: c, Volume: 1000}
}

func dbar(date string, o, h, l, c float64) models.Bar {
	return models.Bar{Date: date, Open: o, High: h, Low: l, Close: c, Volume: 10000}
}

// A limit fills at 11:00 and the target was only touched at 9:00, before the fill. The daily
// range alone would book that win. Played hour by hour it must not.
func TestTargetTouchedBeforeLimitFilledIsNotBooked(t *testing.T) {
	daily := map[string][]models.Bar{"QLD": {
		dbar("2026-03-02", 100, 101, 99, 100),
		dbar("2026-03-03", 100, 104, 96, 97), // limit 99 fills later, the 3% target (101.97) was hit at 9:00
		dbar("2026-03-04", 97, 98, 96, 97),
	}}
	hourly := map[string][]models.Bar{"QLD": {
		hb("2026-03-02 09:00", 100, 101, 99, 100), hb("2026-03-02 15:00", 100, 100, 99, 100),
		hb("2026-03-03 09:00", 100, 104, 100, 102),
		hb("2026-03-03 10:00", 102, 102, 100, 101),
		hb("2026-03-03 11:00", 101, 101, 96, 97),
		hb("2026-03-03 15:00", 97, 97, 96, 97),
		hb("2026-03-04 09:00", 97, 98, 96, 97), hb("2026-03-04 15:00", 97, 97, 96, 97),
	}}
	cfg := strategy.StrategyConfig{AllocationPct: 0.5, PositionCap: 1, TakeProfitPct: 0.03, StopLossPct: 0.5, HoldingWindow: 1, EntryLimitPct: 0.99}
	out, err := Run(Input{
		Signals: []models.Signal{{Symbol: "QLD", Date: "2026-03-03", Entry: 1, Direction: "LONG"}},
		Daily:   daily, Hourly: hourly,
		Dates:  []string{"2026-03-02", "2026-03-03", "2026-03-04"},
		Config: cfg, Capital: 100000,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range out.Trades {
		if tr.ExitReason == models.ExitReasonProfitTarget {
			t.Fatalf("booked a target that was touched before the fill: %+v", tr)
		}
	}
	if len(out.Trades) != 1 || out.Trades[0].ExitReason != models.ExitReasonTimeUp {
		t.Fatalf("trades %+v, want one time-up exit", out.Trades)
	}
	if got := out.Trades[0].EntryPrice; got != 99 {
		t.Errorf("entry %v, want the 99 limit (previous close 100 x 0.99)", got)
	}
	if len(out.DailySessions) != 0 {
		t.Errorf("hourly bars covered every session but %v used the daily bar", out.DailySessions)
	}
}

func TestBarsReplaceHalfHourBeforeTheOpen(t *testing.T) {
	daily := []models.Bar{dbar("2026-03-03", 100, 103, 99, 102)}
	hourly := []models.Bar{hb("2026-03-03 09:00", 90, 120, 80, 101), hb("2026-03-03 10:00", 101, 103, 100, 102)}
	bars, ds := Bars(daily, hourly)
	if len(bars) != 2 || len(ds) != 0 {
		t.Fatalf("bars %v daily %v", bars, ds)
	}
	if bars[0].Open != 100 || bars[0].High != 103 || bars[0].Low != 99 {
		t.Errorf("9:00 bar %+v, want open 100 and range 99..103", bars[0])
	}
}

func TestBarsFallBackToDailyOutsideHourlyCoverage(t *testing.T) {
	daily := []models.Bar{
		dbar("2026-03-02", 10, 11, 9, 10), // before the hourly span: played as one daily bar
		dbar("2026-03-03", 10, 11, 9, 10), // covered
		dbar("2026-03-04", 10, 11, 9, 10), // inside the span with no hourly bar: no bar
		dbar("2026-03-05", 10, 11, 9, 10), // covered
		dbar("2026-03-06", 10, 11, 9, 10), // after the span: daily
	}
	hourly := []models.Bar{hb("2026-03-03 10:00", 10, 11, 9, 10), hb("2026-03-05 10:00", 10, 11, 9, 10)}
	bars, ds := Bars(daily, hourly)
	got := []string{}
	for _, b := range bars {
		got = append(got, b.T)
	}
	want := []string{"2026-03-02 09:00", "2026-03-03 10:00", "2026-03-05 10:00", "2026-03-06 09:00"}
	if len(got) != len(want) {
		t.Fatalf("bar stamps %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bar stamps %v, want %v", got, want)
		}
	}
	if len(ds) != 2 || ds[0] != "2026-03-02" || ds[1] != "2026-03-06" {
		t.Errorf("daily sessions %v", ds)
	}
}

// With no hourly bars a daily bar is one bar: a position cannot exit on the bar that entered it.
func TestDailyOnlyNeverExitsOnItsEntryBar(t *testing.T) {
	daily := map[string][]models.Bar{"VOO": {
		dbar("2026-03-02", 100, 100, 100, 100),
		dbar("2026-03-03", 100, 110, 80, 100), // opens, the range spans both stop and target
		dbar("2026-03-04", 100, 100, 100, 100),
	}}
	cfg := strategy.StrategyConfig{AllocationPct: 0.5, PositionCap: 1, TakeProfitPct: 0.05, StopLossPct: 0.95, HoldingWindow: 5, NextDayOpenEntry: true}
	out, err := Run(Input{
		Signals: []models.Signal{{Symbol: "VOO", Date: "2026-03-02", Entry: 1, Direction: "LONG"}},
		Daily:   daily, Dates: []string{"2026-03-02", "2026-03-03", "2026-03-04"}, Config: cfg, Capital: 100000,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range out.Trades {
		if tr.EntryDate == tr.ExitDate {
			t.Fatalf("exited on its entry session: %+v", tr)
		}
	}
	if len(out.DailySessions["VOO"]) != 3 {
		t.Errorf("daily sessions %v, want all three reported", out.DailySessions)
	}
}

func TestUnsupportedSettingsAreRefusedNotIgnored(t *testing.T) {
	for name, cfg := range map[string]strategy.StrategyConfig{
		"margin":  {UseMargin: true},
		"atr":     {UseATRStop: true},
		"kelly":   {PositionSizing: "kelly"},
		"minterm": {MarginInterestAnnual: 0.07},
	} {
		_, err := Run(Input{Config: cfg, Capital: 1000})
		var u UnsupportedError
		if !errors.As(err, &u) {
			t.Errorf("%s: err %v, want UnsupportedError", name, err)
		}
	}
	_, err := Run(Input{Config: strategy.StrategyConfig{}, Capital: 1000, Dividends: map[string]map[string]float64{"X": {"2026-01-01": 1}}})
	var u UnsupportedError
	if !errors.As(err, &u) {
		t.Errorf("dividends: err %v, want UnsupportedError", err)
	}
}

// The primary may sell a subordinate's position for cash; the stack reports it as preempted and
// gives every member a report of its own.
func TestStackPrimaryPreemptsSubordinate(t *testing.T) {
	daily := map[string][]models.Bar{
		"X": {dbar("2026-03-02", 10, 10, 10, 10), dbar("2026-03-03", 10, 10, 10, 10), dbar("2026-03-04", 10, 10, 10, 10)},
		"Y": {dbar("2026-03-02", 20, 20, 20, 20), dbar("2026-03-03", 20, 20, 20, 20), dbar("2026-03-04", 20, 20, 20, 20)},
	}
	out, err := RunStack(StackInput{
		Members: []Member{
			{ID: "primary", Priority: 0, Config: strategy.StrategyConfig{AllocationPct: 0.5, PositionCap: 1, HoldingWindow: 5},
				Signals: []models.Signal{{Symbol: "Y", Date: "2026-03-03", Entry: 1, Direction: "LONG"}}},
			{ID: "sub", Priority: 1, Config: strategy.StrategyConfig{AllocationPct: 0.9, PositionCap: 1, HoldingWindow: 5},
				Signals: []models.Signal{{Symbol: "X", Date: "2026-03-02", Entry: 1, Direction: "LONG"}}},
		},
		Daily: daily, Dates: []string{"2026-03-02", "2026-03-03", "2026-03-04"}, Capital: 100000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Preempted != 1 {
		t.Fatalf("preempted %d, trades %+v", out.Preempted, out.Trades)
	}
	var sawX bool
	for _, tr := range out.Trades {
		if tr.Symbol == "X" {
			sawX = true
			if tr.ExitReason != models.ExitReasonPreempted || tr.StrategyID != "sub" {
				t.Errorf("X trade %+v, want preempted and attributed to sub", tr)
			}
		}
	}
	if !sawX {
		t.Fatalf("no trade in X: %+v", out.Trades)
	}
	if _, ok := out.PerStrategy["primary"]; !ok {
		t.Errorf("no report for the primary: %v", out.PerStrategy)
	}
	if got := out.PerStrategy["sub"].TotalTrades; got != 1 {
		t.Errorf("sub report counts %d trades, want 1", got)
	}
}

// Leftover cash sits in the default asset, an entry sells some of it, and the park is reported.
func TestStackParksLeftoverCashInTheDefaultAsset(t *testing.T) {
	flat := func(px float64) []models.Bar {
		return []models.Bar{dbar("2026-03-02", px, px, px, px), dbar("2026-03-03", px, px, px, px), dbar("2026-03-04", px, px, px, px), dbar("2026-03-05", px, px, px, px)}
	}
	out, err := RunStack(StackInput{
		Members: []Member{{ID: "s", Config: strategy.StrategyConfig{AllocationPct: 0.5, PositionCap: 1, HoldingWindow: 1},
			Signals: []models.Signal{{Symbol: "X", Date: "2026-03-03", Entry: 1, Direction: "LONG"}}}},
		Daily:        map[string][]models.Bar{"X": flat(100), "PARK": flat(50)},
		Dates:        []string{"2026-03-02", "2026-03-03", "2026-03-04", "2026-03-05"},
		Capital:      100000,
		DefaultAsset: "PARK",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Park.Symbol != "PARK" || out.Park.AvgWeight < 0.4 {
		t.Fatalf("park %+v, want PARK holding most of the cash", out.Park)
	}
	if len(out.Trades) != 1 || out.Trades[0].Shares != 500 {
		t.Fatalf("trades %+v, want one trade of 500 shares funded by selling park shares", out.Trades)
	}
	for _, tr := range out.Trades {
		if tr.Symbol == "PARK" {
			t.Errorf("the park appeared as a trade: %+v", tr)
		}
	}
}

func TestFixedSharesAndDollarSizingAreHonoured(t *testing.T) {
	daily := map[string][]models.Bar{"X": {
		dbar("2026-03-02", 10, 10, 10, 10), dbar("2026-03-03", 10, 10, 10, 10), dbar("2026-03-04", 10, 10, 10, 10),
	}}
	run := func(cfg strategy.StrategyConfig) []models.Trade {
		cfg.PositionCap, cfg.HoldingWindow = 1, 1
		out, err := Run(Input{Signals: []models.Signal{{Symbol: "X", Date: "2026-03-02", Entry: 1, Direction: "LONG"}},
			Daily: daily, Dates: []string{"2026-03-02", "2026-03-03", "2026-03-04"}, Config: cfg, Capital: 100000})
		if err != nil {
			t.Fatal(err)
		}
		return out.Trades
	}
	if tr := run(strategy.StrategyConfig{PositionSizing: "fixed_shares", FixedShares: 1}); len(tr) != 1 || tr[0].Shares != 1 {
		t.Errorf("fixed_shares trades %+v", tr)
	}
	if tr := run(strategy.StrategyConfig{PositionSizing: "fixed_dollar", FixedDollar: 500}); len(tr) != 1 || tr[0].Shares != 50 {
		t.Errorf("fixed_dollar trades %+v", tr)
	}
}

// An entry is cut to the share of the hour's volume the config allows.
func TestMaxBarVolumeShareCutsAnEntry(t *testing.T) {
	daily := map[string][]models.Bar{"X": {
		dbar("2026-03-02", 10, 10, 10, 10), dbar("2026-03-03", 10, 10, 10, 10), dbar("2026-03-04", 10, 10, 10, 10),
	}}
	cfg := strategy.StrategyConfig{AllocationPct: 0.5, PositionCap: 1, HoldingWindow: 1, MaxBarVolumeShare: 0.05} // dbar volume is 10000
	out, err := Run(Input{Signals: []models.Signal{{Symbol: "X", Date: "2026-03-02", Entry: 1, Direction: "LONG"}},
		Daily: daily, Dates: []string{"2026-03-02", "2026-03-03", "2026-03-04"}, Config: cfg, Capital: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Trades) != 1 || out.Trades[0].Shares != 500 {
		t.Fatalf("trades %+v, want 500 shares (5%% of 10000), not the 5000 the allocation would buy", out.Trades)
	}
}
