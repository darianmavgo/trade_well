package study

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func TestMUDecisionTreeRegistration(t *testing.T) {
	s, exists := Get("mu_decision_tree")
	if !exists {
		t.Fatalf("Study 'mu_decision_tree' was not registered")
	}
	if s.ID() != "mu_decision_tree" {
		t.Errorf("Expected ID 'mu_decision_tree', got '%s'", s.ID())
	}
	if s.Name() == "" {
		t.Errorf("Expected non-empty name")
	}
}

func TestMUDecisionTreeExecution(t *testing.T) {
	candidates := []string{
		"../../../data/market_history_hourly.db",
		"data/market_history_hourly.db",
		cliutils.GetDefaultMarketDB(),
	}
	var marketDB string
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			marketDB = c
			break
		}
	}
	if marketDB == "" {
		t.Skip("Market DB not found, skipping integration test")
	}

	tmpDir := t.TempDir()
	resultsDB := filepath.Join(tmpDir, "test_mu_decision_tree.db")

	s, exists := Get("mu_decision_tree")
	if !exists {
		t.Fatalf("Study 'mu_decision_tree' not found")
	}

	s.SetDatabases(marketDB, resultsDB)
	if err := s.Run(); err != nil {
		if strings.Contains(err.Error(), "insufficient daily bars") {
			t.Skipf("the hourly database holds too few sessions for this study: %v", err)
		}
		t.Fatalf("Study Run() failed: %v", err)
	}

	// Verify SQLite DB tables
	db, err := sqlx.Open("sqlite", resultsDB)
	if err != nil {
		t.Fatalf("Failed to open test results DB: %v", err)
	}
	defer db.Close()

	var summaryCount int
	if err := db.Get(&summaryCount, `SELECT count(*) FROM mu_performance_summary`); err != nil {
		t.Fatalf("Failed to query mu_performance_summary: %v", err)
	}
	if summaryCount < 3 {
		t.Errorf("Expected at least 3 models evaluated, found %d", summaryCount)
	}

	var signalsCount int
	if err := db.Get(&signalsCount, `SELECT count(*) FROM mu_daily_signals`); err != nil {
		t.Fatalf("Failed to query mu_daily_signals: %v", err)
	}
	if signalsCount < 500 {
		t.Errorf("Expected >= 500 signal records, found %d", signalsCount)
	}

	// Check that gain_to_drop_ratio beats baseline
	var topRatio, baselineRatio float64
	err = db.QueryRow(`
		SELECT gain_to_drop_ratio, baseline_gain_to_drop_ratio 
		FROM mu_performance_summary 
		WHERE model_name LIKE '%Ultra-Simple%'
	`).Scan(&topRatio, &baselineRatio)
	if err != nil {
		t.Fatalf("Failed to query top ratio: %v", err)
	}

	if topRatio <= baselineRatio {
		t.Errorf("Expected decision tree ratio (%.2f) to beat baseline (%.2f)", topRatio, baselineRatio)
	}

	// Verify HTML report was generated
	htmlPath := filepath.Join(tmpDir, "mu_decision_tree.html")
	if info, err := os.Stat(htmlPath); err != nil || info.Size() == 0 {
		t.Errorf("Expected HTML report to exist and be non-empty at %s", htmlPath)
	}
}
