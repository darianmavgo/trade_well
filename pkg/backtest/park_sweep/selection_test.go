package park_sweep

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseIDs(t *testing.T) {
	for _, arg := range []string{"", "  ", "all", "ALL"} {
		if got := ParseIDs(arg); got != nil {
			t.Errorf("ParseIDs(%q) = %v, want nil", arg, got)
		}
	}
	if got := ParseIDs(" a, b ,,c"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("ParseIDs = %v", got)
	}
}

// Seeding and running with a list touches only the listed rows.
func TestSeedAndRunOnlyListedStrategies(t *testing.T) {
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
			('streak-aaa-down1-aaa','AAA','AAA','AAA','drop',1,2,0,0,'All Regimes',0.10,0,0,0),
			('streak-bbb-down1-bbb','BBB','BBB','BBB','drop',1,2,0,0,'All Regimes',0.10,0,0,0);
	`)
	mustMarket(t, market)
	insertBars(t, market, "GOOGL", []float64{100, 110, 120, 130, 140, 150})
	insertBars(t, market, "AAA", []float64{50, 40, 45, 48, 55, 60})
	insertBars(t, market, "BBB", []float64{50, 40, 45, 48, 55, 60})

	db, err := Open(sweep)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sweep_config (id, start_date, end_date, capital, park_symbol)
		VALUES (1, '2021-10-01', '2021-10-06', 100000, 'GOOGL')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	ids := []string{"STREAK-AAA-DOWN1-AAA", "googl_tree"}
	if _, err := Seed(sweep, settings, market, ids); err != nil {
		t.Fatal(err)
	}
	if err := Run(sweep, market, 2, ids); err != nil {
		t.Fatal(err)
	}
	db, err = Open(sweep)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var seeded []string
	if err := db.Select(&seeded, `SELECT strategy_id FROM sweep_strategy ORDER BY strategy_id`); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(seeded, []string{"streak-aaa-down1-aaa"}) {
		t.Fatalf("seeded %v, want only streak-aaa-down1-aaa", seeded)
	}
	var status string
	if err := db.Get(&status, `SELECT status FROM strategy_run WHERE strategy_id = 'streak-aaa-down1-aaa'`); err != nil || status != "done" {
		t.Fatalf("status %q err %v", status, err)
	}

	// A later full seed adds BBB as pending, and a listed run leaves it pending.
	if _, err := Seed(sweep, settings, market, nil); err != nil {
		t.Fatal(err)
	}
	if err := Run(sweep, market, 2, ids); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&status, `SELECT status FROM strategy_run WHERE strategy_id = 'streak-bbb-down1-bbb'`); err != nil || status != "pending" {
		t.Fatalf("unlisted row status %q err %v, want pending", status, err)
	}
}
