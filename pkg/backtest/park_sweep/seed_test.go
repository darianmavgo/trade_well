package park_sweep

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func TestSeedCopiesThreeKindsAndSkipsLateSymbols(t *testing.T) {
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
			('streak-late-down1-late','LATE','LATE','LATE','drop',1,2,0,0,'All Regimes',0.10,0,0,0);
		INSERT INTO markov_strategy VALUES
			('markov_model_mmm','MMM','MMM','MMM','long','bull',15,0.05,0,0.25,0,0,0);
	`)
	mustMarket(t, market)
	insertBars(t, market, "GOOGL", []float64{100, 110, 120, 130, 140, 150})
	insertBars(t, market, "AAA", []float64{50, 40, 45, 48, 55, 60})
	insertBars(t, market, "MMM", []float64{10, 11, 12, 13, 14, 15})
	insertBars(t, market, "LATE", []float64{9, 9, 9}) // starts after the window opens

	db, err := Open(sweep)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sweep_config (id, start_date, end_date, capital, park_symbol)
		VALUES (1, '2021-10-01', '2021-10-06', 100000, 'GOOGL')`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	counts, err := Seed(sweep, settings, market, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SeedCounts{}
	for _, c := range counts {
		got[c.Kind] = c
	}
	if got["streak"].Rows != 2 || got["streak"].Pending != 1 || got["streak"].Skipped != 1 {
		t.Fatalf("streak counts = %+v", got["streak"])
	}
	if got["markov"].Rows != 1 || got["markov"].Pending != 1 {
		t.Fatalf("markov counts = %+v", got["markov"])
	}
	var status string
	db, err = Open(sweep)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT status FROM strategy_run WHERE strategy_id = 'streak-late-down1-late'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "skipped" {
		t.Fatalf("late status = %s", status)
	}
}

func TestTwoWorkersCannotClaimTheSameRow(t *testing.T) {
	dir := t.TempDir()
	sweep := filepath.Join(dir, "park.db")
	db, err := Open(sweep)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO sweep_strategy (
		strategy_id, kind, name, signal_symbol, trade_symbol, direction, signal_days,
		hold_days, take_profit_pct, stop_loss_pct, regime, target_state,
		allocation_pct, cash_yield, slippage_pct, commission_per_share, next_day_limit)
		VALUES ('only', 'streak', 'only', 'AAA', 'AAA', 'drop', 1, 1, 0, 0, 'All Regimes', '', 0.1, 0, 0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO strategy_run (strategy_id, status) VALUES ('only', 'pending')`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := claimNext(db, nil)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			ids <- id
		}()
	}
	wg.Wait()
	close(ids)
	var got []string
	for id := range ids {
		if id != "" {
			got = append(got, id)
		}
	}
	if len(got) != 1 || got[0] != "only" {
		t.Fatalf("claims = %v", got)
	}
}

func mustExec(t *testing.T, path, sql string) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(sql); err != nil {
		t.Fatal(err)
	}
}

func mustMarket(t *testing.T, path string) {
	t.Helper()
	mustExec(t, path, `
		CREATE TABLE backtest_start (
			idx INTEGER, symbol TEXT, Date TEXT, timeframe TEXT,
			open REAL, high REAL, low REAL, close REAL, volume INTEGER,
			"Adj Close" REAL)`)
}

func insertBars(t *testing.T, path, symbol string, closes []float64) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i, c := range closes {
		date := "2021-10-0" + string(rune('1'+i))
		if symbol == "LATE" {
			date = "2021-10-0" + string(rune('4'+i))
		}
		if _, err := db.Exec(`INSERT INTO backtest_start (idx, symbol, Date, timeframe, open, high, low, close, volume, "Adj Close")
			VALUES (?, ?, ?, '1d', ?, ?, ?, ?, 1000, ?)`,
			i, symbol, date, c, c+1, c-1, c, c); err != nil {
			t.Fatal(err)
		}
	}
}
