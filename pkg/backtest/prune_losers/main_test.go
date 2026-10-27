package prune_losers

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

func TestRunDeletesOnLatestBacktest(t *testing.T) {
	dir := t.TempDir()
	reports := filepath.Join(dir, "reports")
	os.MkdirAll(filepath.Join(reports, "3", "oos"), 0o755)
	// old result says loser, newer run-folder result says winner
	mustExec(t, filepath.Join(reports, "old.db"), summary,
		`INSERT INTO performance_summary VALUES ('s-recovered',0.2,10,'2026-01-01 00:00:00'),('s-loser',0.4,10,'2026-01-01 00:00:00'),('s-winner',0.9,10,'2026-01-01 00:00:00')`)
	mustExec(t, filepath.Join(reports, "3", "streak.db"), summary,
		`INSERT INTO performance_summary VALUES ('s-recovered',0.6,10,'2026-02-01 00:00:00'),('s-idle',0.0,0,'2026-02-01 00:00:00'),('s-edge',0.5,4,'2026-02-01 00:00:00')`)
	mustExec(t, filepath.Join(reports, "3", "oos", "streak.db"), summary,
		`INSERT INTO performance_summary VALUES ('s-winner',0.0,10,'2027-01-01 00:00:00')`) // oos is not read
	mustExec(t, filepath.Join(reports, "board.db"), `CREATE TABLE other(x)`)

	sdb := filepath.Join(dir, "strategies.db")
	mustExec(t, sdb,
		`CREATE TABLE streak_strategy (id TEXT PRIMARY KEY)`,
		`CREATE TABLE hold_strategy (id TEXT PRIMARY KEY)`,
		`INSERT INTO streak_strategy VALUES ('s-recovered'),('s-loser'),('s-winner'),('s-idle'),('s-edge'),('s-never-run')`,
		`INSERT INTO hold_strategy VALUES ('s-loser'),('s-winner')`)

	cfg := Config{StrategiesDB: sdb, ReportsDir: reports, MinWinRate: 0.5, DryRun: true}
	if n, err := Run(cfg); err != nil || n != 3 {
		t.Fatalf("dry run: %d %v", n, err)
	}
	cfg.DryRun = false
	if n, err := Run(cfg); err != nil || n != 3 {
		t.Fatalf("run: %d %v", n, err)
	}
	db, _ := sql.Open("sqlite", sdb)
	defer db.Close()
	var s, h int
	db.QueryRow(`SELECT count(*) FROM streak_strategy`).Scan(&s)
	db.QueryRow(`SELECT count(*) FROM hold_strategy`).Scan(&h)
	if s != 4 || h != 1 {
		t.Fatalf("streak=%d hold=%d, want 4 and 1", s, h)
	}
}
