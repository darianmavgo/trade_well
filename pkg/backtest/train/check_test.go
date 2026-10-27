package train

import (
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func TestCheckLeaksFlagsTreesTrainedPastCutoff(t *testing.T) {
	dir := t.TempDir()
	market, err := storage.OpenSQLite(filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	market.MustExec(`CREATE TABLE backtest_start (symbol TEXT, Date TEXT)`)
	market.MustExec(`INSERT INTO backtest_start VALUES ('AAA','2026-10-02')`)
	market.Close()

	trees, err := storage.OpenSQLite(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	trees.MustExec(`CREATE TABLE tree_model_meta (symbol TEXT PRIMARY KEY, last_date TEXT)`)
	trees.MustExec(`INSERT INTO tree_model_meta VALUES ('OLD','2025-10-01'), ('NEW','2026-09-30')`)
	trees.Close()

	cutoff, leaks, err := CheckLeaks(CheckConfig{MarketDB: filepath.Join(dir, "m.db"), TreeDB: filepath.Join(dir, "t.db")})
	if err != nil {
		t.Fatal(err)
	}
	if cutoff != "2025-10-02" || len(leaks) != 1 || leaks[0].Symbol != "NEW" || leaks[0].Family != "tree" {
		t.Fatalf("cutoff %s leaks %+v", cutoff, leaks)
	}
	report := filepath.Join(dir, "leaks.db")
	if err := SaveLeaks(report, leaks); err != nil {
		t.Fatal(err)
	}
}
