package lastbacktest

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func mustExec(t *testing.T, path string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(s, err)
		}
	}
}

const summary = `CREATE TABLE performance_summary (strategy_id TEXT, win_rate REAL, total_trades INTEGER, created_at TEXT)`

func TestRunRecordsNewestResultDBRelativeToBase(t *testing.T) {
	app := t.TempDir()
	reports := filepath.Join(app, "data", "reports")
	os.MkdirAll(filepath.Join(reports, "3"), 0o755)
	mustExec(t, filepath.Join(reports, "old.db"), summary,
		`INSERT INTO performance_summary VALUES ('s-a',0.2,10,'2026-01-01 00:00:00'),('s-b',0.7,5,'2026-01-01 00:00:00')`)
	mustExec(t, filepath.Join(reports, "3", "streak.db"), summary,
		`INSERT INTO performance_summary VALUES ('s-a',0.6,12,'2026-02-01 00:00:00')`)
	mustExec(t, filepath.Join(reports, "board.db"), `CREATE TABLE other(x)`)

	sdb := filepath.Join(app, "refdata", "strategies.db")
	os.MkdirAll(filepath.Dir(sdb), 0o755)
	cfg := Config{StrategiesDB: sdb, ReportsDir: reports, BaseDir: app, Workers: 2}
	for i := 0; i < 2; i++ { // a second run replaces, not duplicates
		if n, err := Run(cfg); err != nil || n != 2 {
			t.Fatalf("run %d: %d %v", i, n, err)
		}
	}
	db, _ := sql.Open("sqlite", sdb)
	defer db.Close()
	got := map[string]string{}
	rows, _ := db.Query(`SELECT strategy_id, result_db FROM strategy_last_backtest`)
	for rows.Next() {
		var id, p string
		rows.Scan(&id, &p)
		got[id] = p
	}
	if got["s-a"] != "data/reports/3/streak.db" || got["s-b"] != "data/reports/old.db" || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}
