package park_sweep

import (
	"math"
	"path/filepath"
	"testing"
)

func TestRunParksLeftoverCashInGOOGL(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.db")
	market := filepath.Join(dir, "market.db")
	sweep := filepath.Join(dir, "park.db")
	mustExec(t, settings, `
		CREATE TABLE streak_strategy (
			id TEXT PRIMARY KEY, name TEXT, signal_symbol TEXT, trade_symbol TEXT,
			direction TEXT, signal_days INT, hold_days INT, take_profit_pct REAL,
			stop_loss_pct REAL, regime TEXT, allocation_pct REAL, cash_yield REAL,
			slippage_pct REAL, next_day_limit INT);
		CREATE TABLE markov_strategy (
			id TEXT PRIMARY KEY, name TEXT, signal_symbol TEXT, trade_symbol TEXT,
			direction TEXT, target_state TEXT, hold_days INT, take_profit_pct REAL,
			stop_loss_pct REAL, allocation_pct REAL, cash_yield REAL,
			slippage_pct REAL, next_day_limit INT);
		INSERT INTO streak_strategy VALUES
			('streak-aaa-down1-aaa','AAA','AAA','AAA','drop',1,2,0,0,'All Regimes',0.10,0,0,0);
	`)
	mustMarket(t, market)
	insertBars(t, market, "GOOGL", []float64{100, 110, 120, 130, 140, 150})
	insertBars(t, market, "AAA", []float64{50, 40, 45, 48, 55, 60})

	db, err := Open(sweep)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sweep_config (id, start_date, end_date, capital, park_symbol)
		VALUES (1, '2021-10-01', '2021-10-06', 100000, 'GOOGL')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Seed(sweep, settings, market, nil); err != nil {
		t.Fatal(err)
	}
	if err := Run(sweep, market, 2, nil); err != nil {
		t.Fatal(err)
	}

	db, err = Open(sweep)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var status string
	var equity, sleeve, park, weight float64
	var capital float64
	if err := db.QueryRow(`SELECT capital FROM sweep_config WHERE id = 1`).Scan(&capital); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`
		SELECT status, final_equity, sleeve_net, park_contribution, avg_park_weight
		FROM strategy_run WHERE strategy_id = 'streak-aaa-down1-aaa'`).
		Scan(&status, &equity, &sleeve, &park, &weight); err != nil {
		t.Fatal(err)
	}
	if status != "done" {
		t.Fatalf("status = %s", status)
	}
	if math.Abs(park-(equity-capital-sleeve)) > 0.01 {
		t.Fatalf("park contribution %.4f, equity %.4f, sleeve %.4f, capital %.4f", park, equity, sleeve, capital)
	}
	if weight <= 0 {
		t.Fatalf("avg park weight = %v", weight)
	}
}
