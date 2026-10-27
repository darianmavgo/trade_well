package streak_strategy

import (
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func TestValidateRowRejectsBadConfig(t *testing.T) {
	ok := sampleRow()
	if err := ValidateRow(ok); err != nil {
		t.Fatal(err)
	}
	cases := []refdb.StreakStrategy{
		func() refdb.StreakStrategy { r := ok; r.SignalSymbol = "TQQQ'; DROP"; return r }(),
		func() refdb.StreakStrategy { r := ok; r.TradeSymbol = ""; return r }(),
		func() refdb.StreakStrategy { r := ok; r.Direction = "sideways"; return r }(),
		func() refdb.StreakStrategy { r := ok; r.Regime = "QQQ>=SMA50"; return r }(),
		func() refdb.StreakStrategy { r := ok; r.ID = "streak_strategy"; return r }(),
	}
	for _, row := range cases {
		if err := ValidateRow(row); err == nil {
			t.Errorf("expected error for %+v", row)
		}
	}
}

func TestRegisterFrom(t *testing.T) {
	before := registryIDs()
	RegisterFrom(filepath.Join(t.TempDir(), "missing.db"))
	if n := len(registryIDs()); n != len(before) {
		t.Fatalf("missing DB registered %d new strategies", n-len(before))
	}

	empty := filepath.Join(t.TempDir(), "empty.db")
	db, err := refdb.Open(empty)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	RegisterFrom(empty)
	if n := len(registryIDs()); n != len(before) {
		t.Fatalf("empty table registered %d new strategies", n-len(before))
	}

	row := sampleRow()
	db, err = refdb.Open(empty)
	if err != nil {
		t.Fatal(err)
	}
	if err := refdb.UpsertStreakStrategies(db, []refdb.StreakStrategy{row}); err != nil {
		t.Fatal(err)
	}
	db.Close()
	RegisterFrom(empty)

	got, ok := strategy.Get(row.ID)
	if !ok {
		t.Fatalf("%s was not registered", row.ID)
	}
	ss, ok := got.(*Strategy)
	if !ok {
		t.Fatalf("got %T", got)
	}
	if syms := ss.RequiredSymbols(); len(syms) != 2 || syms[0] != "VOO" || syms[1] != "TQQQ" {
		t.Fatalf("RequiredSymbols = %v", syms)
	}
	cfg := ss.DefaultConfig()
	if math.Abs(cfg.StopLossPct-0.92) > 1e-9 || math.Abs(cfg.TakeProfitPct-0.03) > 1e-9 || cfg.TargetPct != 1.03 {
		t.Fatalf("config TP/SL = tp %v target %v stop %v", cfg.TakeProfitPct, cfg.TargetPct, cfg.StopLossPct)
	}
	if cfg.Benchmark != "VOO" || cfg.TradeSymbol != "TQQQ" || cfg.DeclineDays != 2 || cfg.HoldingWindow != 15 {
		t.Fatalf("config symbols/days = %+v", cfg)
	}
}

func TestSQLPipelineSignalDates(t *testing.T) {
	market := filepath.Join(t.TempDir(), "market.db")
	calc := filepath.Join(t.TempDir(), "calc.db")
	voo, tqqq := upSeries()
	gld := downSeries()
	if err := writeMarket(market, voo, tqqq, gld); err != nil {
		t.Fatal(err)
	}

	// VOO closes 10,11,12,13,14,13,14,15 on 2024-01-02..09: a rally of 2+ days ends on 04, 05, 06 and 09.
	dates, _ := runSQL(t, market, calc, sampleRow())
	if want := []string{"2024-01-04", "2024-01-05", "2024-01-06", "2024-01-09"}; !equalStrings(dates, want) {
		t.Fatalf("rally dates %v want %v", dates, want)
	}

	// GLD closes 10,11,12,9,8,11,10.5,10.3: a 2-day drop ends on 06 and 09.
	dropAll := refdb.StreakStrategy{
		ID: "streak-gld-down2-gld", Name: "GLD down2 → GLD",
		SignalSymbol: "GLD", TradeSymbol: "GLD", Direction: "drop",
		SignalDays: 2, HoldDays: 8, TakeProfitPct: 0.02, StopLossPct: 0.05,
		Regime: "All Regimes", AllocationPct: 0.65, CashYield: 0.045,
	}
	all, _ := runSQL(t, market, calc, dropAll)
	if want := []string{"2024-01-06", "2024-01-09"}; !equalStrings(all, want) {
		t.Fatalf("drop dates %v want %v", all, want)
	}

	// Close >= its running 200-bar mean: 8 < 10 on 06 is dropped, 10.3 >= 10.2 on 09 stays.
	dropReg := dropAll
	dropReg.ID = "streak-gld-down2-gld-sma"
	dropReg.Regime = "GLD>=SMA200"
	reg, _ := runSQL(t, market, calc, dropReg)
	if want := []string{"2024-01-09"}; !equalStrings(reg, want) {
		t.Fatalf("regime dates %v want %v", reg, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func runSQL(t *testing.T, market, calc string, row refdb.StreakStrategy) ([]string, map[string]models.Signal) {
	t.Helper()
	s := &Strategy{Row: row}
	s.SetDatabases(market, calc)
	sigs := s.GenerateSignals(nil)
	by := map[string]models.Signal{}
	var dates []string
	for _, sig := range sigs {
		by[sig.Date] = sig
		dates = append(dates, sig.Date)
	}
	return dates, by
}

func sampleRow() refdb.StreakStrategy {
	return refdb.StreakStrategy{
		ID: "streak-voo-up5-tqqq", Name: "VOO up5 → TQQQ",
		SignalSymbol: "VOO", TradeSymbol: "TQQQ", Direction: "rally",
		SignalDays: 2, HoldDays: 15, TakeProfitPct: 0.03, StopLossPct: 0.08,
		Regime: "All Regimes", AllocationPct: 0.65, CashYield: 0.045,
	}
}

func upSeries() (voo, tqqq []models.Bar) {
	// Two-day up streaks on i=2,3,4 and i=7.
	closes := []float64{10, 11, 12, 13, 14, 13, 14, 15}
	trade := []float64{100, 110, 120, 130, 140, 150, 160, 170}
	for i, c := range closes {
		d := dateAt(i)
		voo = append(voo, models.Bar{Idx: i, Symbol: "VOO", Date: d, Open: c, High: c, Low: c, Close: c, Volume: 1000})
		tqqq = append(tqqq, models.Bar{Idx: i, Symbol: "TQQQ", Date: d, Open: trade[i], High: trade[i], Low: trade[i], Close: trade[i], Volume: 1000})
	}
	return voo, tqqq
}

func downSeries() []models.Bar {
	closes := []float64{10, 11, 12, 9, 8, 11, 10.5, 10.3}
	var gld []models.Bar
	for i, c := range closes {
		gld = append(gld, models.Bar{Idx: i, Symbol: "GLD", Date: dateAt(i), Open: c, High: c, Low: c, Close: c, Volume: 1000})
	}
	return gld
}

func dateAt(i int) string {
	return fmt.Sprintf("2024-01-%02d", i+2)
}

func writeMarket(path string, series ...[]models.Bar) error {
	db, err := sqlx.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE backtest_start (
		idx INTEGER, symbol TEXT, Date TEXT, open REAL, high REAL, low REAL, close REAL, volume INTEGER
	)`); err != nil {
		return err
	}
	for _, bars := range series {
		for _, b := range bars {
			if _, err := db.Exec(`INSERT INTO backtest_start (idx, symbol, Date, open, high, low, close, volume) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				b.Idx, b.Symbol, b.Date, b.Open, b.High, b.Low, b.Close, b.Volume); err != nil {
				return err
			}
		}
	}
	return nil
}

func registryIDs() map[string]bool {
	out := map[string]bool{}
	for _, s := range strategy.List() {
		out[s.ID()] = true
	}
	return out
}
