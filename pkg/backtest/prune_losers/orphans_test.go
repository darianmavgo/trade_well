package prune_losers

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func count(t *testing.T, path, q string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(q).Scan(&n); err != nil {
		t.Fatal(q, err)
	}
	return n
}

func TestPruneOrphans(t *testing.T) {
	reports := t.TempDir()
	live := map[string]bool{"s-live": true, "s-live2": true}
	exists := func(id string) bool {
		for _, m := range strings.Split(id, "+") {
			if !live[m] {
				return false
			}
		}
		return true
	}
	tables := []string{
		summary,
		`CREATE TABLE trades (id INTEGER PRIMARY KEY, strategy_id TEXT)`,
		`CREATE TABLE shared_account_audit (combined_id TEXT)`,
		`CREATE TABLE shared_account_priorities (combined_id TEXT, strategy_id TEXT)`,
	}
	mixed := filepath.Join(reports, "mixed.db")
	mustExec(t, mixed, append(tables,
		`INSERT INTO performance_summary VALUES ('s-live',0.6,1,'2026-01-01'),('s-dead',0.6,1,'2026-01-01'),('s-live+s-dead',0.6,1,'2026-01-01'),('s-live+s-live2',0.6,1,'2026-01-01')`,
		`INSERT INTO trades(strategy_id) VALUES ('s-live'),('s-dead'),('s-dead'),('s-live+s-dead')`,
		`INSERT INTO shared_account_audit VALUES ('s-live+s-dead'),('s-live+s-live2')`,
		`INSERT INTO shared_account_priorities VALUES ('s-live+s-dead','s-live'),('s-live+s-live2','s-live')`)...)
	allDead := filepath.Join(reports, "dead.db")
	mustExec(t, allDead, summary, `INSERT INTO performance_summary VALUES ('s-dead',0.6,1,'2026-01-01')`)
	other := filepath.Join(reports, "gridsearch.db") // no performance_summary: not a result DB
	mustExec(t, other, `CREATE TABLE gridsearch_results (strategy_id TEXT)`, `INSERT INTO gridsearch_results VALUES ('s-dead')`)

	cfg := OrphansConfig{ReportsDir: reports, Exists: exists, DryRun: true, Workers: 2}
	res, err := PruneOrphans(cfg)
	if err != nil || len(res.Orphans) != 2 || res.Orphans[0] != "s-dead" {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	if count(t, mixed, `SELECT count(*) FROM trades`) != 4 {
		t.Fatal("dry run deleted rows")
	}

	cfg.DryRun, cfg.RemoveEmpty = false, true
	if _, err := PruneOrphans(cfg); err != nil {
		t.Fatal(err)
	}
	if n := count(t, mixed, `SELECT count(*) FROM performance_summary`); n != 2 {
		t.Fatalf("summary rows %d", n)
	}
	if n := count(t, mixed, `SELECT count(*) FROM trades`); n != 1 {
		t.Fatalf("trades %d", n)
	}
	if n := count(t, mixed, `SELECT count(*) FROM shared_account_priorities`); n != 1 {
		t.Fatalf("priorities %d", n)
	}
	if _, err := os.Stat(allDead); !os.IsNotExist(err) {
		t.Fatal("empty result DB not removed")
	}
	if n := count(t, other, `SELECT count(*) FROM gridsearch_results`); n != 1 {
		t.Fatal("non-result DB touched")
	}
}

func TestVacuumIfWastefulShrinksFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE t(x TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2000; i++ {
		db.Exec(`INSERT INTO t VALUES (?)`, strings.Repeat("x", 500))
	}
	db.Exec(`DELETE FROM t`)
	before, _ := os.Stat(path)
	if err := vacuumIfWasteful(db, OrphansConfig{Vacuum: true}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	if after.Size() >= before.Size()/2 {
		t.Fatalf("file did not shrink: %d -> %d", before.Size(), after.Size())
	}
}
