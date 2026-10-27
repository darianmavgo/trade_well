package backtest

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

func openTestMarket(path string) (*sqlx.DB, error) {
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	if err := storage.EnsureBarTable(db, "backtest_start"); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// holdoutDB builds a real market DB whose daily bars end on lastDate.
func holdoutDB(t *testing.T, lastDates ...string) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "m.db")
	db, err := openTestMarket(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, d := range lastDates {
		if _, err := db.Exec(`INSERT INTO backtest_start (Date, timeframe, open, high, low, close, volume, symbol) VALUES (?, '1d', 1, 1, 1, 1, 1, 'VOO')`, d); err != nil {
			t.Fatal(err)
		}
	}
	conf := DefaultConfig()
	conf.Db = path
	return conf
}

func TestHoldoutSplitUsesLastBar(t *testing.T) {
	conf := holdoutDB(t, "2021-01-04", "2026-10-01")
	inEnd, oosStart, ok, err := holdoutSplit(conf)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if inEnd != "2025-10-01" || oosStart != "2025-10-02" {
		t.Fatalf("got in-sample end %s, oos start %s", inEnd, oosStart)
	}
}

func TestHoldoutSplitHonoursEnd(t *testing.T) {
	conf := holdoutDB(t, "2021-01-04", "2026-10-01")
	conf.End = "2024-06-30"
	conf.HoldoutMonths = 6
	inEnd, _, ok, err := holdoutSplit(conf)
	if err != nil || !ok || inEnd != "2023-12-30" {
		t.Fatalf("inEnd=%s ok=%v err=%v", inEnd, ok, err)
	}
}

func TestHoldoutSplitSkipsShortHistory(t *testing.T) {
	conf := holdoutDB(t, "2025-06-02", "2026-03-02")
	conf.Start = "2025-06-01"
	if _, _, ok, err := holdoutSplit(conf); err != nil || ok {
		t.Fatalf("short history must not split: ok=%v err=%v", ok, err)
	}
}

func TestHoldoutApplies(t *testing.T) {
	c := DefaultConfig()
	if !holdoutApplies(c) {
		t.Fatal("plain run must hold out by default")
	}
	c.Mode = "stack-eval"
	if !holdoutApplies(c) {
		t.Fatal("stack-eval must hold out by default")
	}
	for _, mode := range []string{"stale", "optimized", "covered-call"} {
		c.Mode = mode
		if holdoutApplies(c) {
			t.Fatalf("%s must not hold out", mode)
		}
	}
	c = DefaultConfig()
	c.HoldoutMonths = 0
	if holdoutApplies(c) {
		t.Fatal("-holdout-months 0 must disable it")
	}
	c = DefaultConfig()
	c.SignalsOnly = true
	if holdoutApplies(c) {
		t.Fatal("signals-only scans the live tip")
	}
}
