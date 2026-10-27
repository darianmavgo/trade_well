package price_action_reclaim_strategy

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

func goodRow() refdb.PriceActionReclaimStrategy {
	return refdb.PriceActionReclaimStrategy{
		ID: "par-test", Name: "Test reclaim", Symbol: "TEST", SMAPeriod: 200, SupportBars: 25, ReclaimGap: 5,
		StopCushionPct: 0.005, MaxStopPct: 0.15, TakeProfitPct: 0.10, HoldDays: 20, AllocationPct: 0.10, SlippagePct: 0.0005,
	}
}

// uptrendWithOneReclaim is 300 daily bars of a steady climb in which bar 290 closes under the
// support (the lowest low from 25 to 5 bars back) and bar 291 closes back over it.
func uptrendWithOneReclaim() []models.Bar {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := make([]models.Bar, 300)
	for i := range bars {
		c := 100 + 0.1*float64(i)
		if i == 290 {
			c = 124
		}
		bars[i] = models.Bar{
			Symbol: "TEST", Date: start.AddDate(0, 0, i).Format("2006-01-02"),
			Open: c, High: c + 0.5, Low: c - 0.5, Close: c, Volume: 1000,
		}
	}
	return bars
}

func marketDB(t *testing.T, bars []models.Bar) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "market.db")
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := storage.EnsureBarTable(db, "backtest_start"); err != nil {
		t.Fatal(err)
	}
	for _, b := range bars {
		if _, err := db.Exec(`INSERT INTO backtest_start (symbol, Date, timeframe, open, high, low, close, volume)
			VALUES (?, ?, '1d', ?, ?, ?, ?, ?)`, b.Symbol, b.Date, b.Open, b.High, b.Low, b.Close, b.Volume); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestReclaimEntersOnTheBarThatClosesBackOverSupport(t *testing.T) {
	bars := uptrendWithOneReclaim()
	s := &Strategy{Row: goodRow()}
	s.SetDatabases(marketDB(t, bars), filepath.Join(t.TempDir(), "calc.db"))

	sigs := s.GenerateSignals(map[string][]models.Bar{"TEST": bars})
	if len(sigs) != 1 {
		t.Fatalf("got %d signals, want 1: %+v", len(sigs), sigs)
	}
	sig := sigs[0]
	if sig.Date != bars[291].Date || sig.Symbol != "TEST" || sig.StrategyID != "par-test" {
		t.Fatalf("signal %+v, want TEST on %s", sig, bars[291].Date)
	}
	// Support is the lowest low from bar 266 to bar 286: bar 266's low, 100+0.1*266-0.5.
	wantStop := (100 + 0.1*266 - 0.5) * 0.995
	if math.Abs(sig.StopLoss-wantStop) > 1e-6 {
		t.Fatalf("stop %v, want %v (an absolute price just under support)", sig.StopLoss, wantStop)
	}
}

func TestReclaimSkipsATradeWhoseStopIsTooFarAway(t *testing.T) {
	bars := uptrendWithOneReclaim()
	row := goodRow()
	row.MaxStopPct = 0.001 // support is about 2.7% under the reclaim close
	s := &Strategy{Row: row}
	s.SetDatabases(marketDB(t, bars), filepath.Join(t.TempDir(), "calc.db"))
	if sigs := s.GenerateSignals(map[string][]models.Bar{"TEST": bars}); len(sigs) != 0 {
		t.Fatalf("got %d signals, want none: %+v", len(sigs), sigs)
	}
}

func TestReclaimNeedsTwoHundredFiftyBars(t *testing.T) {
	bars := uptrendWithOneReclaim()[40:] // 260 bars still trade; 200 do not
	short := bars[len(bars)-250+1:]
	s := &Strategy{Row: goodRow()}
	s.SetDatabases(marketDB(t, short), filepath.Join(t.TempDir(), "calc.db"))
	if sigs := s.GenerateSignals(map[string][]models.Bar{"TEST": short}); len(sigs) != 0 {
		t.Fatalf("a symbol with %d bars must not trade, got %+v", len(short), sigs)
	}
}

func TestValidateRejectsBadRows(t *testing.T) {
	for name, mut := range map[string]func(*refdb.PriceActionReclaimStrategy){
		"symbol":      func(r *refdb.PriceActionReclaimStrategy) { r.Symbol = "T'; DROP" },
		"sma":         func(r *refdb.PriceActionReclaimStrategy) { r.SMAPeriod = 1 },
		"gap":         func(r *refdb.PriceActionReclaimStrategy) { r.ReclaimGap = 25 },
		"cushion":     func(r *refdb.PriceActionReclaimStrategy) { r.StopCushionPct = 1 },
		"max stop":    func(r *refdb.PriceActionReclaimStrategy) { r.MaxStopPct = 0 },
		"hold":        func(r *refdb.PriceActionReclaimStrategy) { r.HoldDays = -1 },
		"take profit": func(r *refdb.PriceActionReclaimStrategy) { r.TakeProfitPct = -0.1 },
	} {
		row := goodRow()
		mut(&row)
		if err := (&Strategy{Row: row}).Validate(); err == nil {
			t.Errorf("%s: a bad row validated", name)
		}
	}
	if err := (&Strategy{Row: goodRow()}).Validate(); err != nil {
		t.Fatalf("good row: %v", err)
	}
}

func TestFamilyBuildsRowsOnDemand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO price_action_reclaim_strategy (id, name, symbol) VALUES ('par-spy', 'SPY reclaim', 'spy'), ('par-bad', 'bad', 'S;P')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	RegisterFrom(path)
	for _, id := range []string{"par-spy", "PAR_SPY"} {
		s, ok := strategy.Get(id)
		if !ok || s.ID() != "par-spy" {
			t.Fatalf("Get(%q) = %v, %v", id, s, ok)
		}
		if got := s.(strategy.RequiredSymbolsProvider).RequiredSymbols(); fmt.Sprint(got) != "[SPY]" {
			t.Fatalf("required symbols %v", got)
		}
		if strategy.FamilyOf(s) != "price_action_reclaim" {
			t.Fatalf("family %q", strategy.FamilyOf(s))
		}
	}
	if _, ok := strategy.Get("par-bad"); ok {
		t.Fatal("a row with an unsafe symbol must not build")
	}
	members, ok := strategy.FamilyMembers("price_action_reclaim")
	if !ok || len(members) != 1 {
		t.Fatalf("family members = %d, %v; want only the valid row", len(members), ok)
	}
}
