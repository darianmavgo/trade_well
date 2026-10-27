package export_views

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/backtest/streak_strategy"
	_ "modernc.org/sqlite"
)

// The signal view of a streak row returns the same entry dates as running the row's pipeline.
func TestStrategyViewsMatchPipelineRun(t *testing.T) {
	dir := t.TempDir()
	market := filepath.Join(dir, "market.db")
	db, err := sql.Open("sqlite", market)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE backtest_start (idx INTEGER, symbol TEXT, Date TEXT, open REAL, high REAL, low REAL, close REAL, volume INTEGER)`); err != nil {
		t.Fatal(err)
	}
	voo := []float64{10, 11, 12, 13, 14, 13, 14, 15}
	for i, c := range voo {
		d := fmt.Sprintf("2024-01-%02d", i+2)
		for sym, px := range map[string]float64{"VOO": c, "TQQQ": c * 10} {
			if _, err := db.Exec(`INSERT INTO backtest_start VALUES (?, ?, ?, ?, ?, ?, ?, 1000)`, i, sym, d, px, px, px, px); err != nil {
				t.Fatal(err)
			}
		}
	}

	s := &streak_strategy.Strategy{Row: refdb.StreakStrategy{
		ID: "streak-voo-up2-tqqq", Name: "VOO up2 → TQQQ", SignalSymbol: "VOO", TradeSymbol: "TQQQ", Direction: "rally",
		SignalDays: 2, HoldDays: 15, TakeProfitPct: 0.03, StopLossPct: 0.08, Regime: "All Regimes", AllocationPct: 0.65,
	}}
	s.SetDatabases(market, filepath.Join(dir, "calc.db"))
	var want []string
	for _, sig := range s.GenerateSignals(nil) {
		want = append(want, sig.Date)
	}
	if len(want) == 0 {
		t.Fatal("pipeline made no signals")
	}

	script, views, err := strategy.PipelineViews(s.ID(), s.SQLDir(), s.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if want := "v_streak_voo_up2_tqqq_signals"; !strings.Contains(strings.Join(views, " "), want) {
		t.Fatalf("views %v lack %s", views, want)
	}
	if _, err := db.Exec(script); err != nil {
		t.Fatalf("create views: %v", err)
	}
	rows, err := db.Query(`SELECT date FROM v_streak_voo_up2_tqqq_signals WHERE entry = 1 ORDER BY date`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var d string
		rows.Scan(&d)
		got = append(got, d)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("view dates %v, pipeline dates %v", got, want)
	}
}

func TestStrategyScriptUnknownID(t *testing.T) {
	if _, _, _, err := StrategyScript("no-such-strategy-id"); err == nil {
		t.Fatal("expected an error")
	}
}
