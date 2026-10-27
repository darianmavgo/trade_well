package backtest

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/runner"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
)

func TestValidateAlloc(t *testing.T) {
	if err := validateAlloc(0); err != nil {
		t.Errorf("unset alloc: %v", err)
	}
	if err := validateAlloc(0.10); err != nil {
		t.Errorf("10%% alloc: %v", err)
	}
	if err := validateAlloc(1); err != nil {
		t.Errorf("100%% alloc: %v", err)
	}
	if err := validateAlloc(10); err == nil {
		t.Error("alloc 10 must be rejected; 10%% is 0.10")
	}
}

func TestValidateDefaultAsset(t *testing.T) {
	if err := validateDefaultAsset("", false); err != nil {
		t.Errorf("empty asset: %v", err)
	}
	if err := validateDefaultAsset("GOOGL", true); err != nil {
		t.Errorf("shared GOOGL: %v", err)
	}
	if err := validateDefaultAsset("GOOGL", false); err == nil {
		t.Error("a standalone run must reject -default-asset")
	}
}

func TestDetectAndDownloadMissingData_Disabled(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_market.db")
	tableName := "backtest_start"

	db, err := storage.OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite failed: %v", err)
	}
	if err := storage.EnsureBarTable(db, tableName); err != nil {
		t.Fatalf("EnsureBarTable failed: %v", err)
	}
	db.Close()

	comboStrat := strategy.NewPark("TECL")
	err = runner.DetectAndDownloadMissingData(dbPath, tableName, []strategy.Strategy{comboStrat}, "", false, 5)
	if err == nil {
		t.Fatalf("expected error when autoDownload=false and data missing, got nil")
	}
}
