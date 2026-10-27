package rotation_strategy

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

const sessions = 300

// closeAt is the literal close of each test symbol on session i (0 based).
//
//	UP1   +0.4% a session, all the way
//	FADE  +0.3% a session until session 200, then -3% a session
//	UP2   +0.2% a session
//	FLAT  100 throughout
//	DOWN  -0.2% a session
//	QQQ   +0.3% a session until session 220, then -2% a session (the market gate)
func closeAt(sym string, i int) float64 {
	switch sym {
	case "UP1":
		return 100 * math.Pow(1.004, float64(i))
	case "FADE":
		if i <= 200 {
			return 100 * math.Pow(1.003, float64(i))
		}
		return 100 * math.Pow(1.003, 200) * math.Pow(0.97, float64(i-200))
	case "UP2":
		return 100 * math.Pow(1.002, float64(i))
	case "FLAT":
		return 100
	case "DOWN":
		return 100 * math.Pow(0.998, float64(i))
	case "QQQ":
		if i <= 220 {
			return 100 * math.Pow(1.003, float64(i))
		}
		return 100 * math.Pow(1.003, 220) * math.Pow(0.98, float64(i-220))
	}
	panic(sym)
}

var testSymbols = []string{"UP1", "FADE", "UP2", "FLAT", "DOWN", "QQQ"}

// marketDB writes the literal bars to a temp market database and returns its
// path with the sessions' dates and the same bars as the runner would load them.
func marketDB(t *testing.T) (string, []string, map[string][]models.Bar) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "market.db")
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE backtest_start (idx INTEGER, symbol TEXT, Date TEXT, timeframe TEXT,
		open REAL, high REAL, low REAL, close REAL, volume INTEGER, "Adj Close" REAL)`); err != nil {
		t.Fatal(err)
	}
	var dates []string
	for d := time.Date(2025, 1, 2, 0, 0, 0, 0, time.UTC); len(dates) < sessions; d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			dates = append(dates, d.Format("2006-01-02"))
		}
	}
	bars := map[string][]models.Bar{}
	for _, sym := range testSymbols {
		for i, date := range dates {
			c := closeAt(sym, i)
			if _, err := db.Exec(`INSERT INTO backtest_start (symbol, Date, timeframe, open, high, low, close, volume, "Adj Close")
				VALUES (?, ?, '1d', ?, ?, ?, ?, 1000000, ?)`, sym, date, c, c, c, c, c); err != nil {
				t.Fatal(err)
			}
			bars[sym] = append(bars[sym], models.Bar{Symbol: sym, Date: date, Open: c, High: c, Low: c, Close: c, Volume: 1000000})
		}
	}
	return path, dates, bars
}

func signals(t *testing.T, row refdb.RotationStrategy) ([]models.Signal, []string) {
	t.Helper()
	market, dates, bars := marketDB(t)
	s := &Strategy{Row: row}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	s.SetDatabases(market, filepath.Join(t.TempDir(), "calc.db"))
	return s.GenerateSignals(bars), dates
}

func row() refdb.RotationStrategy {
	return refdb.RotationStrategy{
		ID: "rot-test", Name: "rot test", Symbols: "UP1,FADE,UP2,FLAT,DOWN",
		UniverseSize: 10, TopK: 2, ExitBuffer: 1, MaxWeightPct: 0.5,
		RegimeSymbol: "QQQ", RegimeSMA: 0, AllocationPct: 1,
	}
}

func byKey(sigs []models.Signal) map[string][]models.Signal {
	m := map[string][]models.Signal{}
	for _, s := range sigs {
		m[s.Symbol] = append(m[s.Symbol], s)
	}
	return m
}

func TestRotationBuysTheTopTwoAndSellsAFadingName(t *testing.T) {
	sigs, dates := signals(t, row())
	by := byKey(sigs)

	// 127 sessions of history are needed before a name can be ranked.
	first := dates[126]
	for _, s := range sigs {
		if s.Date < first {
			t.Fatalf("signal on %s before the 127th session %s", s.Date, first)
		}
	}
	if len(by["UP1"]) != 1 || by["UP1"][0].Entry != 1 || by["UP1"][0].Date != first {
		t.Fatalf("UP1 (the strongest) should be bought once on %s, got %+v", first, by["UP1"])
	}
	if len(by["FADE"]) < 2 || by["FADE"][0].Entry != 1 || by["FADE"][0].Date != first {
		t.Fatalf("FADE (second strongest) should be bought on %s then sold, got %+v", first, by["FADE"])
	}
	last := by["FADE"][len(by["FADE"])-1]
	if last.Entry != -1 || last.Date <= dates[200] {
		t.Fatalf("FADE should be sold after its session 200 crash, got %+v", last)
	}
	// Once FADE is out, UP2 is the second strongest and is bought.
	if len(by["UP2"]) == 0 || by["UP2"][0].Entry != 1 || by["UP2"][0].Date <= dates[200] {
		t.Fatalf("UP2 should replace FADE after the crash, got %+v", by["UP2"])
	}
	for _, sym := range []string{"FLAT", "DOWN"} {
		if len(by[sym]) != 0 {
			t.Fatalf("%s is never in the top two, got %+v", sym, by[sym])
		}
	}
	// Two names at a 0.5 cap and allocation 1 is 50% each.
	for _, s := range sigs {
		if s.Entry == 1 && math.Abs(s.AllocationPctOverride-0.5) > 1e-9 {
			t.Fatalf("entry %s %s sized %v, want 0.5", s.Symbol, s.Date, s.AllocationPctOverride)
		}
	}
}

func TestRotationWeightIsCappedAndSplitByTopK(t *testing.T) {
	r := row()
	r.TopK, r.MaxWeightPct, r.AllocationPct = 4, 0.2, 1
	if got := (&Strategy{Row: r}).weight(); math.Abs(got-0.2) > 1e-9 {
		t.Fatalf("4 names at a 0.2 cap: weight %v, want 0.2", got)
	}
	r.TopK, r.MaxWeightPct, r.AllocationPct = 5, 0.5, 0.5
	if got := (&Strategy{Row: r}).weight(); math.Abs(got-0.1) > 1e-9 {
		t.Fatalf("0.5 sleeve over 5 names: weight %v, want 0.1", got)
	}
}

func TestRotationMarketGateSellsEverythingWhenTheMarketBreaks(t *testing.T) {
	r := row()
	r.RegimeSMA = 50
	sigs, dates := signals(t, r)
	by := byKey(sigs)

	up1 := by["UP1"]
	if len(up1) != 2 || up1[0].Entry != 1 || up1[1].Entry != -1 {
		t.Fatalf("UP1 should be bought, then sold when QQQ breaks its 50 session average, got %+v", up1)
	}
	// QQQ peaks on session 220 and falls 2% a session, so it is under its 50
	// session average within a handful of sessions.
	if up1[1].Date <= dates[220] || up1[1].Date > dates[235] {
		t.Fatalf("UP1 sold on %s, want a few sessions after %s", up1[1].Date, dates[220])
	}
	for sym, ss := range by {
		if ss[len(ss)-1].Entry != -1 {
			t.Fatalf("%s is still held at the end of a falling market: %+v", sym, ss)
		}
	}
}

func TestRotationValidateRefusesAnUnrunnableRow(t *testing.T) {
	for name, mutate := range map[string]func(*refdb.RotationStrategy){
		"no candidates":        func(r *refdb.RotationStrategy) { r.Symbols = "" },
		"quote in a symbol":    func(r *refdb.RotationStrategy) { r.Symbols = "AAA,B'); DROP TABLE x;--" },
		"top_k zero":           func(r *refdb.RotationStrategy) { r.TopK = 0 },
		"universe under top_k": func(r *refdb.RotationStrategy) { r.UniverseSize = 1 },
		"weight over one":      func(r *refdb.RotationStrategy) { r.MaxWeightPct = 1.5 },
		"negative exit buffer": func(r *refdb.RotationStrategy) { r.ExitBuffer = -1 },
		"unsafe regime symbol": func(r *refdb.RotationStrategy) { r.RegimeSMA = 50; r.RegimeSymbol = "Q'Q" },
	} {
		r := row()
		mutate(&r)
		if err := (&Strategy{Row: r}).Validate(); err == nil {
			t.Errorf("%s: Validate accepted %+v", name, r)
		}
	}
}

func TestRotationFamilyReadsARowFromTheReferenceDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO rotation_strategy (id, name, symbols, universe_size, top_k, exit_buffer, max_weight_pct,
		regime_symbol, regime_sma, allocation_pct, cash_yield, slippage_pct)
		VALUES ('rotation-top3', 'Top 3', 'AAA,BBB,CCC', 3, 3, 1, 0.5, 'QQQ', 0, 1, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	f := NewFamily(func() string { return path })
	s, ok := f.Lookup("Rotation_Top3") // the family match ignores case and punctuation
	if !ok || s.ID() != "rotation-top3" {
		t.Fatalf("lookup failed: %v %v", s, ok)
	}
	if ids, err := f.IDs(); err != nil || fmt.Sprint(ids) != "[rotation-top3]" {
		t.Fatalf("IDs = %v, %v", ids, err)
	}
	req := s.(*Strategy).RequiredSymbols()
	if fmt.Sprint(req) != "[AAA BBB CCC]" {
		t.Fatalf("RequiredSymbols = %v", req)
	}
}
