package runner

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
)

// writeResults makes a results database with the two tables PickSet reads.
func writeResults(t *testing.T, path string, summaries [][4]any, trades map[string][]string) {
	t.Helper()
	db, err := storage.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE performance_summary (run_id INTEGER, strategy_id TEXT, win_rate REAL, total_trades INTEGER, cagr REAL, max_drawdown_pct REAL);
		CREATE TABLE trades (strategy_id TEXT, symbol TEXT, entry_date TEXT, exit_date TEXT, net_pnl REAL, return_pct REAL, hold_days INTEGER)`); err != nil {
		t.Fatal(err)
	}
	for _, s := range summaries {
		if _, err := db.Exec(`INSERT INTO performance_summary VALUES (1, ?, ?, ?, 0.1, ?)`, s[0], s[1], s[2], s[3]); err != nil {
			t.Fatal(err)
		}
	}
	for id, days := range trades {
		for _, d := range days {
			if _, err := db.Exec(`INSERT INTO trades VALUES (?, 'X', ?, ?, 1, 0.01, 3)`, id, d, d); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestPickSet(t *testing.T) {
	dir := t.TempDir()
	in, oos := filepath.Join(dir, "in.db"), filepath.Join(dir, "oos.db")
	// a and b win the most but enter on the same days. c enters on other days. d has a low win rate. e has too few held-out trades.
	writeResults(t, in, [][4]any{{"a", 0.90, 40, 0.05}, {"b", 0.88, 40, 0.05}, {"c", 0.80, 40, 0.05}, {"d", 0.60, 40, 0.05}, {"e", 0.85, 40, 0.05}},
		map[string][]string{
			"a": {"2024-01-02", "2024-01-03", "2024-01-04", "2024-01-05"},
			"b": {"2024-01-02", "2024-01-03", "2024-01-04", "2024-01-05"},
			"c": {"2024-02-01", "2024-02-02", "2024-02-05", "2024-02-06"},
			"d": {"2024-03-01"}, "e": {"2024-04-01"},
		})
	writeResults(t, oos, [][4]any{{"a", 0.90, 10, 0.05}, {"b", 0.90, 10, 0.05}, {"c", 0.80, 10, 0.05}, {"d", 0.90, 10, 0.05}, {"e", 0.90, 3, 0.05}},
		map[string][]string{
			"a": {"2025-01-02"}, "b": {"2025-01-02"}, "c": {"2025-02-03"}, "d": {"2025-03-03"}, "e": {"2025-04-01"},
		})

	opts := PickOptions{Sources: []PickSource{{Name: "one", InSampleDB: in, HeldOutDB: oos}}, OutDB: filepath.Join(dir, "pick.db"),
		Count: 10, MinWinRate: 0.75, MinTrades: 30, MinOOSTrades: 8, TopN: 50, MaxOverlap: 0.30}
	res, err := PickSet(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidates != 3 {
		t.Fatalf("candidates %d, want a, b and c", res.Candidates)
	}
	// a first (best), b is skipped for overlapping a completely, c is chosen.
	if len(res.Picks) != 2 || res.Picks[0].ID != "a" || res.Picks[1].ID != "c" {
		t.Fatalf("picks %+v", res.Picks)
	}
	if res.Picks[1].MaxOverlap != 0 {
		t.Fatalf("c overlap %v", res.Picks[1].MaxOverlap)
	}
	if res.Set == nil || res.Set.CollisionDays != 0 || res.Set.Trades != 10 || res.Set.WinRate != 1 {
		t.Fatalf("set %+v", res.Set)
	}

	// A loose overlap limit admits b as well.
	opts.MaxOverlap = 1
	res, err = PickSet(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(len(res.Picks)); got != "3" {
		t.Fatalf("picks with no overlap limit: %s", got)
	}

	// A second family adds its strategy f, which enters on days of its own, and one of its
	// strategies shares a's days completely (g) so is skipped.
	in2, oos2 := filepath.Join(dir, "in2.db"), filepath.Join(dir, "oos2.db")
	writeResults(t, in2, [][4]any{{"f", 0.80, 40, 0.05}, {"g", 0.76, 40, 0.05}},
		map[string][]string{
			"f": {"2024-05-01", "2024-05-02", "2024-05-03", "2024-05-06"},
			"g": {"2024-01-02", "2024-01-03", "2024-01-04", "2024-01-05"},
		})
	writeResults(t, oos2, [][4]any{{"f", 0.80, 10, 0.05}, {"g", 0.80, 10, 0.05}},
		map[string][]string{"f": {"2025-05-01"}, "g": {"2025-01-02"}})
	opts.MaxOverlap = 0.30
	opts.Sources = append(opts.Sources, PickSource{Name: "two", InSampleDB: in2, HeldOutDB: oos2})
	res, err = PickSet(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range res.Picks {
		ids = append(ids, p.Family+":"+p.ID)
	}
	if got := fmt.Sprint(ids); got != "[one:a one:c two:f]" {
		t.Fatalf("picks across two families: %s", got)
	}
}
