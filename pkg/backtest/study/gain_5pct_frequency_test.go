package study

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func TestClassifyTicker(t *testing.T) {
	tests := []struct {
		sym      string
		isLev    bool
		factor   string
		category string
	}{
		{"SOXL", true, "3x Bull", "Leveraged ETF"},
		{"TECL", true, "3x Bull", "Leveraged ETF"},
		{"SPXU", true, "-3x Bear", "Leveraged Inverse ETF"},
		{"BITX", true, "2x Bull", "Leveraged ETF"},
		{"UVXY", true, "1.5x Vol", "Leveraged Volatility ETF"},
		{"CONL", true, "2x Bull", "Leveraged ETF"},
		{"VOO", false, "1x Unleveraged", "1x ETF"},
		{"SPY", false, "1x Unleveraged", "1x ETF"},
		{"GLD", false, "1x Unleveraged", "1x ETF"},
		{"AAPL", false, "1x Non-Leveraged", "Stock / Equity"},
		{"NVDA", false, "1x Non-Leveraged", "Stock / Equity"},
	}

	for _, tc := range tests {
		info := ClassifyTicker(tc.sym)
		if info.IsLeveraged != tc.isLev {
			t.Errorf("Symbol %s: expected isLeveraged=%v, got %v", tc.sym, tc.isLev, info.IsLeveraged)
		}
		if info.Factor != tc.factor {
			t.Errorf("Symbol %s: expected factor=%s, got %s", tc.sym, tc.factor, info.Factor)
		}
		if info.Category != tc.category {
			t.Errorf("Symbol %s: expected category=%s, got %s", tc.sym, tc.category, info.Category)
		}
	}
}

func TestComputeTickerStats(t *testing.T) {
	// Day 0: Open 100, High 102, Low 99, Close 100
	// Day 1: Open 100, High 106, Low 100, Close 106 (+6% gain) -> 5%+ gain
	// Day 2: Open 106, High 108, Low 104, Close 105 (-0.94%) -> neutral
	// Day 3: Open 105, High 112, Low 105, Close 111.3 (+6% gain) -> 5%+ gain
	// Day 4: Open 111.3, High 112, Low 104, Close 104.6 (-6.02% drop) -> 5%+ drop
	bars := []rawBar{
		{Date: "2025-01-01", Open: 100.0, High: 102.0, Low: 99.0, Close: 100.0},
		{Date: "2025-01-02", Open: 100.0, High: 106.0, Low: 100.0, Close: 106.0},
		{Date: "2025-01-03", Open: 106.0, High: 108.0, Low: 104.0, Close: 105.0},
		{Date: "2025-01-04", Open: 105.0, High: 112.0, Low: 105.0, Close: 111.3},
		{Date: "2025-01-05", Open: 111.3, High: 112.0, Low: 104.0, Close: 104.6},
	}

	stat := computeTickerStats("SOXL", bars)

	if stat.Symbol != "SOXL" {
		t.Fatalf("expected symbol SOXL, got %s", stat.Symbol)
	}
	if stat.IsLeveragedETF != 1 {
		t.Fatalf("expected is_leveraged_etf=1, got %d", stat.IsLeveragedETF)
	}
	if stat.TotalSessions != 5 {
		t.Fatalf("expected total sessions 5, got %d", stat.TotalSessions)
	}
	if stat.DaysGain5Pct != 2 {
		t.Fatalf("expected 2 days with 5%%+ gain, got %d", stat.DaysGain5Pct)
	}
	if stat.DaysDrop5Pct != 1 {
		t.Fatalf("expected 1 day with 5%%+ drop, got %d", stat.DaysDrop5Pct)
	}
	// 2 gains / 1 drop = ratio 2.0
	if stat.GainDropRatio < 1.99 || stat.GainDropRatio > 2.01 {
		t.Fatalf("expected gain/drop ratio 2.0, got %.2f", stat.GainDropRatio)
	}
	// 2 out of 4 returns = 50.00%
	if stat.Gain5PctFrequency < 49.9 || stat.Gain5PctFrequency > 50.1 {
		t.Fatalf("expected gain frequency ~50.0%%, got %.2f%%", stat.Gain5PctFrequency)
	}
	// 1 out of 4 returns = 25.00%
	if stat.Drop5PctFrequency < 24.9 || stat.Drop5PctFrequency > 25.1 {
		t.Fatalf("expected drop frequency ~25.0%%, got %.2f%%", stat.Drop5PctFrequency)
	}
	if stat.MaxDayGainPct < 5.9 || stat.MaxDayGainPct > 6.1 {
		t.Fatalf("expected max gain ~6.0%%, got %.2f%%", stat.MaxDayGainPct)
	}
	if stat.MaxDayDropPct > -5.9 || stat.MaxDayDropPct < -6.2 {
		t.Fatalf("expected max drop ~ -6.02%%, got %.2f%%", stat.MaxDayDropPct)
	}
}

func TestGain5PctFrequencyStudy_Run(t *testing.T) {
	tempDir := t.TempDir()
	marketDBPath := filepath.Join(tempDir, "market_test.db")
	resultsDBPath := filepath.Join(tempDir, "results_test.db")

	// Create test source market DB
	db, err := sqlx.Open("sqlite", marketDBPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}

	createSQL := `
	CREATE TABLE backtest_start (
		Date TEXT,
		timeframe TEXT DEFAULT '1d',
		open REAL,
		high REAL,
		low REAL,
		close REAL,
		symbol TEXT
	);
	`
	if _, err := db.Exec(createSQL); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}

	// Insert bars for SOXL (leveraged) and AAPL (equity)
	insertSQL := `
	INSERT INTO backtest_start (Date, timeframe, open, high, low, close, symbol) VALUES
	('2025-01-01', '1d', 100, 102, 99, 100, 'SOXL'),
	('2025-01-02', '1d', 100, 107, 100, 107, 'SOXL'),
	('2025-01-03', '1d', 107, 114, 107, 114, 'SOXL'),
	('2025-01-01', '1d', 150, 151, 149, 150, 'AAPL'),
	('2025-01-02', '1d', 150, 152, 149, 151, 'AAPL'),
	('2025-01-03', '1d', 151, 152, 150, 151.5, 'AAPL');
	`
	if _, err := db.Exec(insertSQL); err != nil {
		t.Fatalf("failed to insert test data: %v", err)
	}
	db.Close()

	studyInstance, ok := Get("gain_5pct_frequency")
	if !ok {
		t.Fatalf("study gain_5pct_frequency not found in registry")
	}

	studyInstance.SetDatabases(marketDBPath, resultsDBPath)
	if err := studyInstance.Run(); err != nil {
		t.Fatalf("study run failed: %v", err)
	}

	// Verify results DB
	resDB, err := sqlx.Open("sqlite", resultsDBPath)
	if err != nil {
		t.Fatalf("failed to open results db: %v", err)
	}
	defer resDB.Close()

	var rowCount int
	if err := resDB.Get(&rowCount, "SELECT count(*) FROM gain_5pct_frequency"); err != nil {
		t.Fatalf("failed to count rows: %v", err)
	}
	if rowCount != 2 {
		t.Fatalf("expected 2 rows, got %d", rowCount)
	}

	var topSymbol string
	var gainDropRatio float64
	var daysDrop int
	if err := resDB.QueryRow("SELECT symbol, gain_drop_ratio, days_drop_5pct FROM gain_5pct_frequency WHERE rank = 1").Scan(&topSymbol, &gainDropRatio, &daysDrop); err != nil {
		t.Fatalf("failed to query rank 1: %v", err)
	}
	if topSymbol != "SOXL" {
		t.Fatalf("expected rank 1 to be SOXL, got %s", topSymbol)
	}

	// Verify summary table
	var summaryCount int
	if err := resDB.Get(&summaryCount, "SELECT count(*) FROM gain_5pct_summary"); err != nil {
		t.Fatalf("failed to query summary table: %v", err)
	}
	if summaryCount < 2 {
		t.Fatalf("expected summary rows, got %d", summaryCount)
	}

	// Verify HTML report was generated
	htmlPath := filepath.Join(tempDir, "gain_5pct_frequency.html")
	if _, err := os.Stat(htmlPath); os.IsNotExist(err) {
		t.Fatalf("expected html report to exist at %s", htmlPath)
	}
}
