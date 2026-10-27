package scan_market

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open memory db: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("failed to init schema: %v", err)
	}
	return db
}

func TestBuySignalsRoundTrip(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	id, err := InsertBuySignal(db, &BuySignal{
		ScanRunID:     "test-run",
		StackID:       "stack-a",
		StrategyID:    "sig-test",
		StrategyName:  "Test",
		Symbol:        "VOO",
		Direction:     "LONG",
		AsOfDate:      "2026-09-19",
		NextSession:   "2026-09-22",
		Price:         100,
		Close:         99.5,
		BuyLimit:      100.1,
		TakeProfit:    105,
		StopLoss:      95,
		AllocationPct: 0.2,
		HoldDays:      8,
		Status:        BuySignalNew,
		RawJSON:       `{"symbol":"VOO"}`,
		Notes:         "unit",
		CreatedAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatalf("id=%d", id)
	}
	var symbol, status string
	if err := db.QueryRow(`SELECT symbol, status FROM scan_market_buy_signal WHERE id = ?`, id).Scan(&symbol, &status); err != nil {
		t.Fatal(err)
	}
	if symbol != "VOO" || status != BuySignalNew {
		t.Fatalf("symbol=%q status=%q", symbol, status)
	}
}

func TestUpsertSignalDayOneRowPerStrategyPerDay(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()
	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	id1, err := UpsertSignalDay(db, "sig-voo-buy-tecl", "2026-09-15", "VOO", "NO_BUY", 0, "first", "EVALUATED", now)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := UpsertSignalDay(db, "sig-voo-buy-tecl", "2026-09-15", "TECL", "BUY", 90, "second", "PENDING", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("expected same row id, got %d then %d", id1, id2)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM scan_market_signal WHERE strategy_name = 'sig-voo-buy-tecl'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row, got %d", n)
	}
	var typ, sym string
	if err := db.QueryRow(`SELECT signal_type, symbol FROM scan_market_signal WHERE id = ?`, id2).Scan(&typ, &sym); err != nil {
		t.Fatal(err)
	}
	if typ != "BUY" || sym != "TECL" {
		t.Fatalf("expected upserted BUY/TECL, got %s/%s", typ, sym)
	}
}

func TestRecentListsBuysAndNoBuysNewestFirst(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, d := range []string{"2026-10-09", "2026-10-12", "2026-10-02"} {
		if _, err := UpsertSignalDay(db, "streak-shpu-down3", d, "", "NO_BUY", 0, "no buy signal as of "+d, "NO_BUY", now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO scan_market_buy_signal (scan_run_id, stack_id, strategy_id, symbol, as_of_date, next_session, close, buy_limit, take_profit, stop_loss, allocation_pct, hold_days, status, created_at)
		VALUES ('r1', 'a+b', 'markov_model_mull', 'MULL', '2026-10-12', '2026-10-13', 21.7, 21.5, 22.8, 0, 0.25, 15, 'NEW', '2026-10-12T22:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	res, err := RecentFor(db, now, 7, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buys) != 1 || res.Buys[0].Symbol != "MULL" || res.Buys[0].BuyLimit != 21.5 {
		t.Fatalf("buys %+v", res.Buys)
	}
	if len(res.NoBuys) != 2 || res.NoBuys[0].Date != "2026-10-12" || res.NoBuys[1].Date != "2026-10-09" {
		t.Fatalf("no-buys %+v, want 10-12 then 10-09 (10-02 is outside 7 days)", res.NoBuys)
	}
}

func TestRecentForKeepsOnlyTheDeployedStrategies(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, id := range []string{"streak-shpu-down3", "streak-voo-buy-tecl", "markov_model_mull+streak-shpu-down3"} {
		if _, err := UpsertSignalDay(db, id, "2026-10-12", "", "NO_BUY", 0, "no buy", "NO_BUY", now); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"rotation-old", "markov_model_mull"} {
		if _, err := db.Exec(`INSERT INTO scan_market_buy_signal (scan_run_id, stack_id, strategy_id, symbol, as_of_date, status, created_at) VALUES ('r', '', ?, 'X', '2026-10-12', 'NEW', '2026-10-12T22:00:00Z')`, id); err != nil {
			t.Fatal(err)
		}
	}
	res, err := RecentFor(db, now, 7, []string{"markov_model_mull", "streak-shpu-down3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Buys) != 1 || res.Buys[0].StrategyID != "markov_model_mull" || len(res.NoBuys) != 1 || res.NoBuys[0].Strategy != "streak-shpu-down3" {
		t.Fatalf("got %+v", res)
	}
	if res.Hidden != 3 {
		t.Fatalf("hidden %d, want 3 (rotation-old, streak-voo-buy-tecl, and the whole-stack row)", res.Hidden)
	}
	if all, _ := RecentFor(db, now, 7, nil); len(all.Buys) != 2 || len(all.NoBuys) != 3 || all.Hidden != 0 {
		t.Fatalf("unfiltered %+v, want everything", all)
	}
}
