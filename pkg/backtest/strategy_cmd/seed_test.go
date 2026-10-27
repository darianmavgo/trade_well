package strategy_cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

type hmmRowAll struct {
	ID    string   `db:"id"`
	Name  string   `db:"name"`
	Rule  string   `db:"rule"`
	Stop  float64  `db:"stop_loss_pct"`
	Hold  int      `db:"hold_days"`
	Win   *float64 `db:"win_rate"`
	Label *string  `db:"source_label"`
}

func seedRef(t *testing.T) string {
	t.Helper()
	ref := filepath.Join(t.TempDir(), "strategies.db")
	db, err := refdb.Open(ref)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// A quote and a semicolon in a name, a NULL, a fraction that is not exact in binary.
	if _, err := db.Exec(`INSERT INTO hmm_strategy (id, name, hmm_symbol, trade_symbol, rule, stress_prob, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct, win_rate, source_label) VALUES
		('b', 'It''s a; test', 'QQQ', 'TECL', 'normal_filter', 0.5, 8, 0, 0.1, 0.2, 0.045, 0.0005, 0.123456789012345, 'x'),
		('a', 'plain', 'QQQ', 'SOXL', 'calm_filter', 0.5, 3, 0, 0.12, 0.2, 0.045, 0.0005, NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	return ref
}

func readHMM(t *testing.T, path string) []hmmRowAll {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows []hmmRowAll
	if err := db.Select(&rows, `SELECT id, name, rule, stop_loss_pct, hold_days, win_rate, source_label FROM hmm_strategy ORDER BY id`); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestSeedReloadsToTheSameRows(t *testing.T) {
	ref := seedRef(t)
	var buf bytes.Buffer
	if err := Seed(&buf, ref, "hmm_strategy", nil); err != nil {
		t.Fatal(err)
	}
	script := buf.String()
	if !strings.Contains(script, "CREATE TABLE IF NOT EXISTS hmm_strategy") || strings.Index(script, "'a'") > strings.Index(script, "'b'") {
		t.Fatalf("seed must create the table and order rows by id:\n%s", script)
	}
	fresh := filepath.Join(t.TempDir(), "fresh.db")
	db, err := storage.OpenSQLite(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(script); err != nil {
		t.Fatalf("the seed does not run: %v\n%s", err, script)
	}
	// Running it twice changes nothing (INSERT OR REPLACE).
	if _, err := db.Exec(script); err != nil {
		t.Fatal(err)
	}
	db.Close()
	want, got := readHMM(t, ref), readHMM(t, fresh)
	if !reflect.DeepEqual(want, got) || len(got) != 2 {
		t.Fatalf("rows after reload:\n got  %+v\n want %+v", got, want)
	}
}

func TestSeedPicksRowsAndRefusesBadInput(t *testing.T) {
	ref := seedRef(t)
	var buf bytes.Buffer
	if err := Seed(&buf, ref, "hmm_strategy", []string{"b"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "'plain'") || !strings.Contains(buf.String(), "It''s a; test") {
		t.Fatalf("-ids b must dump only b:\n%s", buf.String())
	}
	for name, c := range map[string]struct {
		table string
		ids   []string
	}{
		"unknown id":        {"hmm_strategy", []string{"b", "nope"}},
		"no such table":     {"no_such_table", nil},
		"not an identifier": {"hmm_strategy; DROP TABLE hmm_strategy", nil},
		"empty table":       {"", nil},
		"table with no id":  {"bundle_meta", nil},
	} {
		if err := Seed(&bytes.Buffer{}, ref, c.table, c.ids); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if len(readHMM(t, ref)) != 2 {
		t.Fatal("a refused dump changed the table")
	}
}

func TestSeedFileWritesOnceAndNeedsForceToReplace(t *testing.T) {
	ref := seedRef(t)
	path := filepath.Join(t.TempDir(), "sql", "seed", "hmm_strategy.sql")
	var out bytes.Buffer
	if err := SeedFile(&out, ref, "hmm_strategy", nil, path, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err == nil || !strings.Contains(out.String(), "INSERT OR REPLACE") {
		t.Fatalf("a dry run must print the seed and write nothing (%v)", err)
	}
	if err := SeedFile(&bytes.Buffer{}, ref, "hmm_strategy", nil, path, false, false); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if err := SeedFile(&bytes.Buffer{}, ref, "hmm_strategy", nil, path, false, false); err == nil {
		t.Fatal("an existing seed must not be replaced without -force")
	}
	if err := SeedFile(&bytes.Buffer{}, ref, "hmm_strategy", nil, path, true, false); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(path); !bytes.Equal(first, again) {
		t.Fatal("dumping the same rows twice must give the same file")
	}
}
