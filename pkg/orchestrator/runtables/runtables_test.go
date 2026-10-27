package runtables

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func memory(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSplitKeepsSemicolonsInQuotesAndDropsComments(t *testing.T) {
	got := Split("-- a comment; with a semicolon\nCREATE TABLE t (a TEXT);\nINSERT INTO t VALUES ('x;y'); -- tail\n  ;\nSELECT 1")
	want := []string{"CREATE TABLE t (a TEXT)", "INSERT INTO t VALUES ('x;y')", "SELECT 1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRewriteNamesOnlyTheRunsTablesAndDropsMainTemp(t *testing.T) {
	r := Begin(nil, []string{"slice_a", "slice_a_long", "slice_b"})
	got := r.Rewrite("SELECT * FROM main.slice_a JOIN temp.slice_b JOIN slice_a_long JOIN real_table WHERE x = 'slice_a'")
	for _, name := range []string{"slice_a", "slice_b", "slice_a_long"} {
		if !strings.Contains(got, r.Table(name)) {
			t.Errorf("%s was not rewritten: %s", name, got)
		}
	}
	if strings.Contains(got, "main.") || strings.Contains(got, "temp.") || !strings.Contains(got, "real_table") {
		t.Errorf("unexpected rewrite: %s", got)
	}
}

// Two runs of the same stages write to different tables, so one never sees the other's rows.
func TestRunsDoNotShareTables(t *testing.T) {
	ctx := context.Background()
	db := memory(t)
	a := Begin(db, []string{"slice_a"})
	b := Begin(db, []string{"slice_a"})
	if a.ID == b.ID {
		t.Fatal("two runs got the same id")
	}
	for i, r := range []*Run{a, b} {
		if err := r.Exec(ctx, `DROP TABLE IF EXISTS slice_a; CREATE TABLE slice_a (v INTEGER); INSERT INTO slice_a VALUES (`+string(rune('1'+i))+`)`); err != nil {
			t.Fatal(err)
		}
	}
	for i, r := range []*Run{a, b} {
		var v int
		if err := r.QueryRow(ctx, `SELECT v FROM slice_a`).Scan(&v); err != nil || v != i+1 {
			t.Fatalf("run %d read %d (%v), want %d", i, v, err, i+1)
		}
	}
}

func TestPruneKeepsTheNewestRuns(t *testing.T) {
	ctx := context.Background()
	db := memory(t)
	var ids []string
	for i := 0; i < 4; i++ {
		r := Begin(db, []string{"slice_a", "slice_b"})
		ids = append(ids, r.ID)
		if err := r.Exec(ctx, `CREATE TABLE slice_a (v INTEGER); CREATE TABLE slice_b (v INTEGER)`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE unrelated__r1 (v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := Prune(ctx, db, []string{"slice_a", "slice_b"}, 2); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'slice_%'`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("tables left = %d (%v), want 4 (two runs x two tables)", n, err)
	}
	for _, old := range ids[:2] {
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE ?`, "%"+old).Scan(&n); err != nil || n != 0 {
			t.Fatalf("run %s was not pruned", old)
		}
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name = 'unrelated__r1'`).Scan(&n); err != nil || n != 1 {
		t.Fatal("Prune touched a table that is not one of the named slice tables")
	}
}

func TestInsertRowsBatchesUnderTheValueLimit(t *testing.T) {
	ctx := context.Background()
	db := memory(t)
	if _, err := db.Exec(`CREATE TABLE t (a INTEGER, b TEXT, c REAL)`); err != nil {
		t.Fatal(err)
	}
	rows := make([][]any, 1000) // 3000 values: more than one statement can bind
	for i := range rows {
		rows[i] = []any{i, "x", 1.5}
	}
	if err := InsertRows(ctx, db, "t", []string{"a", "b", "c"}, rows); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t`).Scan(&n); err != nil || n != 1000 {
		t.Fatalf("rows = %d (%v), want 1000", n, err)
	}
	if err := InsertRows(ctx, db, "t", []string{"a", "b", "c"}, [][]any{{1, "short"}}); err == nil {
		t.Fatal("a row with the wrong number of values was accepted")
	}
}

func TestFindRecentReturnsOnlyAFinishedRunOfTheSameKeyWithinMaxAge(t *testing.T) {
	ctx := context.Background()
	db := memory(t)
	tables := []string{"slice_a", "slice_done"}
	a := BeginKeyed(db, tables, "aaa")
	if err := a.Exec(ctx, `CREATE TABLE slice_a (v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := FindRecent(ctx, db, tables, "slice_done", "aaa", time.Minute); err != nil || ok {
		t.Fatalf("an unfinished run (no marker) was returned: %v %v", ok, err)
	}
	if err := a.Exec(ctx, `CREATE TABLE slice_done (v INTEGER)`); err != nil {
		t.Fatal(err)
	}
	got, ok, err := FindRecent(ctx, db, tables, "slice_done", "aaa", time.Minute)
	if err != nil || !ok || got.ID != a.ID {
		t.Fatalf("finished run not found: %v %v %v", got, ok, err)
	}
	if _, ok, _ := FindRecent(ctx, db, tables, "slice_done", "bbb", time.Minute); ok {
		t.Fatal("a run of another key was returned")
	}
	if _, ok, _ := FindRecent(ctx, db, tables, "slice_done", "aaa", 0); ok {
		t.Fatal("a zero MaxAge must never reuse")
	}
	var v int
	if err := got.QueryRow(ctx, `SELECT COUNT(*) FROM slice_a`).Scan(&v); err != nil {
		t.Fatalf("the reused run does not read its own tables: %v", err)
	}
}
