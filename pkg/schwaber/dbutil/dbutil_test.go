package dbutil

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenAppliesPragmas(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "sub", "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
}

func TestAddColumnUpgradesAnExistingTableWithoutLosingRows(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT); INSERT INTO t (name) VALUES ('kept')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // the second call must change nothing
		if err := AddColumn(db, "t", "profit_taker", "REAL"); err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
	}
	var name string
	var pt sql.NullFloat64
	if err := db.QueryRow(`SELECT name, profit_taker FROM t`).Scan(&name, &pt); err != nil {
		t.Fatalf("the new column must be readable: %v", err)
	}
	if name != "kept" || pt.Valid {
		t.Errorf("row = %q %+v; want the old row with a NULL new column", name, pt)
	}
	// It also works inside a transaction, which has a single connection.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := AddColumn(tx, "t", "stop_loss", "REAL"); err != nil {
		t.Errorf("inside a transaction: %v", err)
	}
	_ = tx.Commit()
	if _, err := db.Exec(`INSERT INTO t (name, profit_taker, stop_loss) VALUES ('new', 10.82, 8.24)`); err != nil {
		t.Errorf("insert naming both new columns: %v", err)
	}
}

func TestAddColumnIsANoOpWithoutTheTableAndRejectsBadNames(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := AddColumn(db, "missing", "c", "TEXT"); err != nil {
		t.Errorf("a table that does not exist yet is fine: %v", err)
	}
	if _, err := db.Exec(`SELECT * FROM missing`); err == nil {
		t.Error("AddColumn must not create the table")
	}
	for _, bad := range [][2]string{{"t; DROP TABLE t", "c"}, {"t", "c d"}, {"", "c"}} {
		if err := AddColumn(db, bad[0], bad[1], "TEXT"); err == nil {
			t.Errorf("%q.%q should be rejected", bad[0], bad[1])
		}
	}
}
