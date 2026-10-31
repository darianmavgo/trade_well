package refdb

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

func TestStackIDsAndLabels(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "strategies.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first, err := AddStack(db, "Win Rate Ten", []string{"a", " b ", "c", "b", ""}, "first")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "stack-001" || first.StrategyIDs != "a+b+c" || !reflect.DeepEqual(first.Members(), []string{"a", "b", "c"}) {
		t.Fatalf("first stack %+v", first)
	}
	second, err := AddStack(db, "Other", []string{"x"}, "")
	if err != nil || second.ID != "stack-002" {
		t.Fatalf("second stack %+v, %v", second, err)
	}

	// Found by id, by label, and by a label typed loosely.
	for _, key := range []string{"stack-001", "STACK-001", "Win Rate Ten", "win-rate_ten"} {
		st, ok, err := StackByName(db, key)
		if err != nil || !ok || st.ID != "stack-001" {
			t.Fatalf("lookup %q: %+v %v %v", key, st, ok, err)
		}
	}
	if _, ok, _ := StackByName(db, "nothing"); ok {
		t.Fatal("found a stack that does not exist")
	}

	// Labels: under 30 characters, no '+' or ',', and unique whatever the case.
	for _, label := range []string{"", strings.Repeat("x", 30), "a+b", "a,b", "win rate ten"} {
		if _, err := AddStack(db, label, []string{"a"}, ""); err == nil {
			t.Fatalf("label %q was accepted", label)
		}
	}
	if _, err := AddStack(db, strings.Repeat("x", 29), []string{"a"}, ""); err != nil {
		t.Fatalf("a 29 character label: %v", err)
	}
	if _, err := AddStack(db, "Empty", []string{" ", ""}, ""); err == nil {
		t.Fatal("a stack with no strategies was accepted")
	}
	// The table itself refuses what AddStack refuses, for inserts made with SQL.
	if _, err := db.Exec(`INSERT INTO stack (label, strategy_ids) VALUES (?, 'a')`, strings.Repeat("y", 30)); err == nil {
		t.Fatal("SQL insert of a 30 character label was accepted")
	}
	// Members are joined with "+" only: a "," list is refused.
	if _, err := db.Exec(`INSERT INTO stack (label, strategy_ids) VALUES ('Commas', 'a,b')`); err == nil {
		t.Fatal("a comma list was accepted")
	}
	// A SQL insert gets its id generated too.
	if _, err := db.Exec(`INSERT INTO stack (label, strategy_ids) VALUES ('By SQL', 'a+b')`); err != nil {
		t.Fatal(err)
	}
	st, ok, _ := StackByName(db, "by sql")
	if !ok || st.ID != "stack-004" {
		t.Fatalf("SQL stack %+v", st)
	}
}

func TestStackTableUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "strategies.db")
	old, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	// The table as it was before ids were generated.
	if _, err := old.Exec(`CREATE TABLE stack (name TEXT PRIMARY KEY, id TEXT NOT NULL UNIQUE, note TEXT NOT NULL DEFAULT '');
		INSERT INTO stack VALUES ('Voo Streak Eleven', 'p+s1+s2', 'kept note'), ('A name that is much too long to keep whole', 'q+r', '')`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stacks, err := Stacks(db)
	if err != nil || len(stacks) != 2 {
		t.Fatalf("stacks %+v, %v", stacks, err)
	}
	st, ok, _ := StackByName(db, "voo streak eleven")
	if !ok || st.StrategyIDs != "p+s1+s2" || st.Note != "kept note" || st.ID == "" {
		t.Fatalf("migrated stack %+v", st)
	}
	if long := stacks[0].Label; len(long) != 29 {
		t.Fatalf("long label %q", long)
	}
	// Opening again changes nothing.
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db2.Close()
	if again, _ := Stacks(db); len(again) != 2 {
		t.Fatalf("second open: %+v", again)
	}
}
