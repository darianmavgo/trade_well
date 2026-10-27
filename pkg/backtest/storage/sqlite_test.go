package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

func TestValidateTableName(t *testing.T) {
	valid := []string{"backtest_start", "trades", "equity_curve", "test_123", ""}
	for _, name := range valid {
		if err := ValidateTableName(name); err != nil {
			t.Errorf("expected %q to be valid, got %v", name, err)
		}
	}

	invalid := []string{"table; DROP TABLE users;", "table name", "foo-bar", "test--comment", "table'"}
	for _, name := range invalid {
		if err := ValidateTableName(name); err == nil {
			t.Errorf("expected %q to be rejected, got nil error", name)
		}
	}
}

func TestTradePersistenceAndSQLAggregates(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sqlitetest_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	db, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	trades := []models.Trade{
		{
			Symbol:                "AAPL",
			OrderType:             "limit",
			EntryDate:             "2023-01-05",
			EntryPrice:            150.0,
			ExitDate:              "2023-01-10",
			ExitPrice:             165.0,
			ExitReason:            models.ExitReasonProfitTarget,
			Shares:                100,
			NetPnL:                1500.0,
			ReturnPct:             0.10,
			HoldDays:              5,
			CommissionPaid:        2.0,
			MaxAdverseExcursion:   -0.01,
			MaxFavorableExcursion: 0.11,
		},
		{
			Symbol:                "MSFT",
			OrderType:             "limit",
			EntryDate:             "2023-01-06",
			EntryPrice:            240.0,
			ExitDate:              "2023-01-11",
			ExitPrice:             228.0,
			ExitReason:            models.ExitReasonStopLoss,
			Shares:                50,
			NetPnL:                -600.0,
			ReturnPct:             -0.05,
			HoldDays:              5,
			CommissionPaid:        2.0,
			MaxAdverseExcursion:   -0.05,
			MaxFavorableExcursion: 0.01,
		},
	}

	stratID := "test-strat"
	if err := SaveTrades(db, stratID, trades); err != nil {
		t.Fatalf("failed to save trades: %v", err)
	}

	report, err := FetchTradeSummaryStats(db, stratID)
	if err != nil {
		t.Fatalf("failed to fetch trade summary stats: %v", err)
	}

	if report.TotalTrades != 2 {
		t.Errorf("expected 2 total trades, got %d", report.TotalTrades)
	}
	if report.WinningTrades != 1 {
		t.Errorf("expected 1 winning trade, got %d", report.WinningTrades)
	}
	if report.LosingTrades != 1 {
		t.Errorf("expected 1 losing trade, got %d", report.LosingTrades)
	}
	if report.WinRate != 0.5 {
		t.Errorf("expected 0.5 win rate, got %f", report.WinRate)
	}
	if report.NetProfit != 900.0 {
		t.Errorf("expected net profit 900.0, got %f", report.NetProfit)
	}
	if report.TotalCommissionPaid != 4.0 {
		t.Errorf("expected total commission 4.0, got %f", report.TotalCommissionPaid)
	}
	if report.ProfitFactor != 2.5 {
		t.Errorf("expected profit factor 2.5, got %f", report.ProfitFactor)
	}
}

func TestCreateUniqueDB(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "unique_db_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stratID := "bb-capitulation"

	// First call should create bb-capitulation.db
	p1, db1, err := CreateUniqueDB(tempDir, stratID)
	if err != nil {
		t.Fatalf("first CreateUniqueDB failed: %v", err)
	}
	defer db1.Close()
	expected1 := filepath.Join(tempDir, "bb-capitulation.db")
	if p1 != expected1 {
		t.Errorf("expected %s, got %s", expected1, p1)
	}

	// Second call should create bb-capitulation_2.db
	p2, db2, err := CreateUniqueDB(tempDir, stratID)
	if err != nil {
		t.Fatalf("second CreateUniqueDB failed: %v", err)
	}
	defer db2.Close()
	expected2 := filepath.Join(tempDir, "bb-capitulation_2.db")
	if p2 != expected2 {
		t.Errorf("expected %s, got %s", expected2, p2)
	}

	// Third call should create bb-capitulation_3.db
	p3, db3, err := CreateUniqueDB(tempDir, stratID)
	if err != nil {
		t.Fatalf("third CreateUniqueDB failed: %v", err)
	}
	defer db3.Close()
	expected3 := filepath.Join(tempDir, "bb-capitulation_3.db")
	if p3 != expected3 {
		t.Errorf("expected %s, got %s", expected3, p3)
	}

	// Different strategy should start at other-strat.db
	pOther, dbOther, err := CreateUniqueDB(tempDir, "other-strat")
	if err != nil {
		t.Fatalf("other strategy CreateUniqueDB failed: %v", err)
	}
	defer dbOther.Close()
	expectedOther := filepath.Join(tempDir, "other-strat.db")
	if pOther != expectedOther {
		t.Errorf("expected %s, got %s", expectedOther, pOther)
	}

	// Test concurrent creation to verify atomic exclusivity without collisions
	concurrentCount := 10
	results := make([]string, concurrentCount)
	var wg sync.WaitGroup

	for i := 0; i < concurrentCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			p, db, err := CreateUniqueDB(tempDir, "concurrent-test")
			if err != nil {
				t.Errorf("concurrent CreateUniqueDB error: %v", err)
				return
			}
			db.Close()
			results[idx] = p
		}(i)
	}
	wg.Wait()

	seen := make(map[string]bool)
	for _, p := range results {
		if p == "" {
			t.Errorf("empty path returned in concurrent test")
			continue
		}
		if seen[p] {
			t.Errorf("duplicate db path generated concurrently: %s", p)
		}
		seen[p] = true
	}
}

func TestPerformanceReportPersistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "perf_report_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "perf.db")
	db, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	report := models.PerformanceReport{
		StartDate:          "2022-01-01",
		EndDate:            "2025-12-31",
		TotalTradingDays:   1000,
		TotalCalendarYears: 4.0,
		InitialCapital:     100000.0,
		FinalEquity:        185000.0,
		NetProfit:          85000.0,
		TotalReturnPct:     0.85,
		CAGR:               0.166,
		SharpeRatio:        1.45,
		SortinoRatio:       2.10,
		CalmarRatio:        1.25,
		MaxDrawdownPct:     0.132,
		MaxDrawdownDollars: 15000.0,
		TotalTrades:        45,
		WinningTrades:      30,
		LosingTrades:       15,
		WinRate:            0.6667,
		ProfitFactor:       2.3,
	}

	stratID := "test-quant-strat"
	if err := SavePerformanceReport(db, stratID, report); err != nil {
		t.Fatalf("SavePerformanceReport failed: %v", err)
	}

	var count int
	err = db.Get(&count, "SELECT COUNT(*) FROM performance_summary WHERE strategy_id = ?", stratID)
	if err != nil {
		t.Fatalf("failed to query performance_summary: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 row in performance_summary, got %d", count)
	}

	var netProfit float64
	err = db.Get(&netProfit, "SELECT net_profit FROM performance_summary WHERE strategy_id = ?", stratID)
	if err != nil {
		t.Fatalf("failed to query net_profit: %v", err)
	}
	if netProfit != 85000.0 {
		t.Errorf("expected net_profit 85000.0, got %f", netProfit)
	}
}

func TestGetSymbolDateCoverage(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cov_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "cov.db")
	db, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	if err := EnsureBarTable(db, "backtest_start"); err != nil {
		t.Fatalf("EnsureBarTable failed: %v", err)
	}

	// 1. Non-existent symbol coverage should return 0 count and empty dates
	cov, err := GetSymbolDateCoverage(db, "backtest_start", "AAPL")
	if err != nil {
		t.Fatalf("GetSymbolDateCoverage failed: %v", err)
	}
	if cov.BarCount != 0 || cov.MinDate != "" || cov.MaxDate != "" {
		t.Errorf("expected empty coverage for AAPL, got %+v", cov)
	}

	// 2. Insert some bars
	bars := []models.Bar{
		{Symbol: "AAPL", Date: "2024-01-02", Open: 180, Close: 185},
		{Symbol: "AAPL", Date: "2024-01-03", Open: 185, Close: 184},
		{Symbol: "AAPL", Date: "2024-01-04", Open: 184, Close: 186},
	}
	if err := UpsertBars(db, "backtest_start", bars); err != nil {
		t.Fatalf("UpsertBars failed: %v", err)
	}

	// 3. Verify coverage after inserting bars
	cov, err = GetSymbolDateCoverage(db, "backtest_start", "AAPL")
	if err != nil {
		t.Fatalf("GetSymbolDateCoverage failed: %v", err)
	}
	if cov.BarCount != 3 {
		t.Errorf("expected 3 bars, got %d", cov.BarCount)
	}
	if cov.MinDate != "2024-01-02" {
		t.Errorf("expected minDate 2024-01-02, got %s", cov.MinDate)
	}
	if cov.MaxDate != "2024-01-04" {
		t.Errorf("expected maxDate 2024-01-04, got %s", cov.MaxDate)
	}
}

func TestFetchRecentBars(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "recent_bars_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test_recent.db")
	db, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	defer db.Close()

	if err := EnsureBarTable(db, "backtest_start"); err != nil {
		t.Fatalf("EnsureBarTable failed: %v", err)
	}

	var bars []models.Bar
	for i := 1; i <= 10; i++ {
		dateStr := fmt.Sprintf("2024-01-%02d", i)
		bars = append(bars, models.Bar{
			Symbol: "SPY",
			Date:   dateStr,
			Close:  float64(400 + i),
		})
	}
	if err := UpsertBars(db, "backtest_start", bars); err != nil {
		t.Fatalf("UpsertBars failed: %v", err)
	}

	// Fetch only the latest 3 bars
	bySymbol, dates, err := FetchRecentBars(db, "backtest_start", []string{"SPY"}, 3)
	if err != nil {
		t.Fatalf("FetchRecentBars failed: %v", err)
	}

	spyBars := bySymbol["SPY"]
	if len(spyBars) != 3 {
		t.Fatalf("expected 3 bars, got %d", len(spyBars))
	}
	if len(dates) != 3 {
		t.Fatalf("expected 3 dates, got %d", len(dates))
	}

	// Verify chronological order of the returned 3 most recent bars
	if spyBars[0].Date != "2024-01-08" || spyBars[2].Date != "2024-01-10" {
		t.Errorf("expected range 2024-01-08 to 2024-01-10, got %s to %s", spyBars[0].Date, spyBars[2].Date)
	}
}
