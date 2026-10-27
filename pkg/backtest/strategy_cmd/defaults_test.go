package strategy_cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/refdb"
	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// defaultsWorld makes a reference database with one hmm row and a gridsearch database with the
// columns `strategy defaults` reads, both real SQLite files in a temp folder.
func defaultsWorld(t *testing.T) (ref, grid string) {
	t.Helper()
	dir := t.TempDir()
	ref, grid = filepath.Join(dir, "strategies.db"), filepath.Join(dir, "gridsearch.db")
	db, err := refdb.Open(ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO hmm_strategy (id, name, hmm_symbol, trade_symbol, rule, stress_prob, hold_days, take_profit_pct, stop_loss_pct, allocation_pct, cash_yield, slippage_pct)
		VALUES ('hmm-normal-tecl', 'TECL', 'QQQ', 'TECL', 'normal_filter', 0.5, 3, 0, 0.12, 0.2, 0.045, 0.0005)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	g, err := storage.OpenSQLite(grid)
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	for _, q := range []string{
		`CREATE TABLE gridsearch_results (id INTEGER PRIMARY KEY, strategy_id TEXT, label TEXT, hold_days INTEGER, take_profit_pct REAL, stop_loss_pct REAL)`,
		`INSERT INTO gridsearch_results VALUES (65, 'hmm-normal-tecl', 'TECL/Hold-3d/TP+0%/SL-12%', 3, 0, 0.12)`,
		`INSERT INTO gridsearch_results VALUES (218, 'hmm_normal_tecl', 'TECL/Hold-8d/TP+0%/SL-2%', 8, 0, 0.02)`,
		`INSERT INTO gridsearch_results VALUES (300, 'hmm-calm-soxl', 'SOXL/Hold-5d/TP+4%/SL-6%', 5, 0.04, 0.06)`,
	} {
		if _, err := g.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return ref, grid
}

func hmmRow(t *testing.T, ref string) (hold int, tp, sl float64, rule string) {
	t.Helper()
	db, err := refdb.Open(ref)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT hold_days, take_profit_pct, stop_loss_pct, rule FROM hmm_strategy WHERE id = 'hmm-normal-tecl'`).Scan(&hold, &tp, &sl, &rule); err != nil {
		t.Fatal(err)
	}
	return
}

func TestDefaultsCopiesOnlyTheThreeExitParameters(t *testing.T) {
	ref, grid := defaultsWorld(t)
	var out bytes.Buffer

	if err := SetDefaults(&out, ref, grid, "hmm-normal-tecl", "TECL/Hold-8d/TP+0%/SL-2%", SeedOptions{Skip: true}, true); err != nil {
		t.Fatal(err)
	}
	if hold, _, sl, _ := hmmRow(t, ref); hold != 3 || sl != 0.12 {
		t.Fatalf("a dry run wrote: hold %d stop %v", hold, sl)
	}
	if !strings.Contains(out.String(), "3 -> 8") || !strings.Contains(out.String(), "dry run") {
		t.Fatalf("dry run output:\n%s", out.String())
	}

	out.Reset()
	if err := SetDefaults(&out, ref, grid, "hmm-normal-tecl", "TECL/Hold-8d/TP+0%/SL-2%", SeedOptions{Skip: true}, false); err != nil {
		t.Fatal(err)
	}
	hold, tp, sl, rule := hmmRow(t, ref)
	if hold != 8 || tp != 0 || sl != 0.02 || rule != "normal_filter" {
		t.Fatalf("row after: hold %d tp %v sl %v rule %s", hold, tp, sl, rule)
	}
	out.Reset()
	if err := SetDefaults(&out, ref, grid, "hmm-normal-tecl", "TECL/Hold-8d/TP+0%/SL-2%", SeedOptions{Skip: true}, false); err != nil || !strings.Contains(out.String(), "no change") {
		t.Fatalf("a second run must say no change: %v\n%s", err, out.String())
	}
}

func TestDefaultsRefusesWhatDoesNotMatch(t *testing.T) {
	ref, grid := defaultsWorld(t)
	g, err := storage.OpenSQLite(grid)
	if err != nil {
		t.Fatal(err)
	}
	// The same label twice with different parameters cannot be told apart.
	for _, q := range []string{
		`INSERT INTO gridsearch_results VALUES (400, 'hmm-normal-tecl', 'TECL/Hold-9d/TP+0%/SL-3%', 9, 0, 0.03)`,
		`INSERT INTO gridsearch_results VALUES (401, 'hmm-normal-tecl', 'TECL/Hold-9d/TP+0%/SL-3%', 9, 0, 0.04)`,
		`INSERT INTO gridsearch_results VALUES (402, 'hmm-normal-tecl', 'TECL/Hold-3d/TP+0%/SL-12%', 3, 0, 0.12)`,
	} {
		if _, err := g.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	g.Close()
	var out bytes.Buffer
	for name, c := range map[string]struct{ id, label string }{
		"a label of another strategy": {"hmm-normal-tecl", "SOXL/Hold-5d/TP+4%/SL-6%"},
		"no such label":               {"hmm-normal-tecl", "TECL/Hold-99d/TP+0%/SL-1%"},
		"no such strategy":            {"hmm-normal-nope", "TECL/Hold-8d/TP+0%/SL-2%"},
		"an ambiguous label":          {"hmm-normal-tecl", "TECL/Hold-9d/TP+0%/SL-3%"},
		"no label given":              {"hmm-normal-tecl", ""},
	} {
		if err := SetDefaults(&out, ref, grid, c.id, c.label, SeedOptions{Skip: true}, false); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	if hold, _, sl, _ := hmmRow(t, ref); hold != 3 || sl != 0.12 {
		t.Fatalf("a refused call changed the row: hold %d stop %v", hold, sl)
	}
	// The same label twice with the same parameters is fine (the baseline repeats a grid row).
	if err := SetDefaults(&out, ref, grid, "hmm-normal-tecl", "TECL/Hold-3d/TP+0%/SL-12%", SeedOptions{Skip: true}, false); err != nil {
		t.Fatalf("identical duplicates must be accepted: %v", err)
	}
}

func TestDefaultsTakesTheStrategyFromTheSweep(t *testing.T) {
	ref, grid := defaultsWorld(t)
	var out bytes.Buffer
	// No id: the sweep row's strategy_id (written with underscores here) finds the stored row.
	if err := SetDefaults(&out, ref, grid, "", "TECL/Hold-8d/TP+0%/SL-2%", SeedOptions{Skip: true}, false); err != nil {
		t.Fatal(err)
	}
	if hold, _, sl, _ := hmmRow(t, ref); hold != 8 || sl != 0.02 {
		t.Fatalf("row after: hold %d stop %v", hold, sl)
	}
	g, err := storage.OpenSQLite(grid)
	if err != nil {
		t.Fatal(err)
	}
	// The same label for two strategies cannot be resolved without -id.
	if _, err := g.Exec(`INSERT INTO gridsearch_results VALUES (500, 'hmm-calm-soxl', 'TECL/Hold-8d/TP+0%/SL-2%', 8, 0, 0.02)`); err != nil {
		t.Fatal(err)
	}
	g.Close()
	if err := SetDefaults(&out, ref, grid, "", "TECL/Hold-8d/TP+0%/SL-2%", SeedOptions{Skip: true}, false); err == nil || !strings.Contains(err.Error(), "-id") {
		t.Fatalf("a label shared by two strategies must ask for -id: %v", err)
	}
	if err := SetDefaults(&out, ref, grid, "hmm-normal-tecl", "TECL/Hold-8d/TP+0%/SL-2%", SeedOptions{Skip: true}, false); err != nil {
		t.Fatalf("with -id the shared label resolves: %v", err)
	}
	if err := SetDefaults(&out, ref, grid, "", "no such label", SeedOptions{Skip: true}, false); err == nil {
		t.Fatal("an unknown label must be refused")
	}
}

func TestDefaultsRecreatesTheSeedFromTheDatabase(t *testing.T) {
	ref, grid := defaultsWorld(t)
	label := "TECL/Hold-8d/TP+0%/SL-2%"
	var out bytes.Buffer

	// A dry run recreates nothing.
	seed := filepath.Join(t.TempDir(), "hmm_strategy.sql")
	if err := SetDefaults(&out, ref, grid, "", label, SeedOptions{Path: seed}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(seed); err == nil {
		t.Fatal("a dry run must not write the seed")
	}

	// An explicit path is recreated, replacing what was there, and holds the new values.
	if err := os.WriteFile(seed, []byte("-- old hand-written seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDefaults(&out, ref, grid, "", label, SeedOptions{Path: seed}, false); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(seed)
	if strings.Contains(string(body), "hand-written") || !strings.Contains(string(body), "'hmm-normal-tecl'") || !strings.Contains(string(body), ", 8, 0.0, 0.02,") {
		t.Fatalf("seed after:\n%s", body)
	}

	// -no-seed leaves the file as it is.
	if err := os.WriteFile(seed, []byte("-- keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDefaults(&out, ref, grid, "", "TECL/Hold-3d/TP+0%/SL-12%", SeedOptions{Path: seed, Skip: true}, false); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(seed); string(body) != "-- keep\n" {
		t.Fatalf("-no-seed changed the seed: %q", body)
	}

	// With no path, the default sql/seed/<table>.sql is recreated only if it exists.
	dir := t.TempDir()
	old, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(old)
	out.Reset()
	if err := SetDefaults(&out, ref, grid, "", label, SeedOptions{}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sql", "seed", "hmm_strategy.sql")); err == nil || !strings.Contains(out.String(), "no seed file") {
		t.Fatalf("no default seed exists, so none may be made:\n%s", out.String())
	}
	if err := os.MkdirAll(filepath.Join(dir, "sql", "seed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sql", "seed", "hmm_strategy.sql"), []byte("-- old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDefaults(&out, ref, grid, "", "TECL/Hold-3d/TP+0%/SL-12%", SeedOptions{}, false); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "sql", "seed", "hmm_strategy.sql")); !strings.Contains(string(body), "INSERT OR REPLACE") || !strings.Contains(string(body), ", 3, 0.0, 0.12,") {
		t.Fatalf("the existing default seed was not recreated:\n%s", body)
	}
}
