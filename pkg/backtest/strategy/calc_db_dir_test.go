package strategy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// A strategy must open its calc DB even when that DB's directory
// does not exist yet. Before the fix this failed with "unable to open database
// file", the pipeline returned no signals, and a real entry was reported as
// NO_SIGNAL.
func TestOpenAttachedCalcDBCreatesMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	marketPath := filepath.Join(dir, "market.db")
	m, err := sqlx.Open("sqlite", marketPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Exec(`CREATE TABLE backtest_start (symbol TEXT, date TEXT, close REAL)`); err != nil {
		t.Fatal(err)
	}
	m.Close()

	calcPath := filepath.Join(dir, "does", "not", "exist", "livescan.db")
	if _, err := os.Stat(filepath.Dir(calcPath)); err == nil {
		t.Fatal("test setup: directory must not exist yet")
	}
	db, err := openAttachedCalcDB(marketPath, calcPath)
	if err != nil {
		t.Fatalf("openAttachedCalcDB with a missing directory: %v", err)
	}
	defer db.Close()
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM backtest_start`); err != nil {
		t.Fatalf("market table not visible through the attached view: %v", err)
	}
}
