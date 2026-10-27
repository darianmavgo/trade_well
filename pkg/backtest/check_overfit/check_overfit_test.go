package check_overfit

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestVerdicts(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE walk_forward_summary (
		strategy_id TEXT, folds INT, is_sharpe REAL, oos_sharpe REAL,
		is_return_pct REAL, oos_return_pct REAL, oos_trades INT,
		oos_positive_folds INT, trials INT
	)`); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO walk_forward_summary VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	rows := [][]any{
		{"holds", 4, 1.0, 0.8, 0.10, 0.08, 20, 3, 4},
		{"decays", 4, 1.0, 0.10, 0.20, 0.01, 20, 1, 4},
		{"curve", 4, 1.2, 0.10, 0.40, 0.01, 20, 1, 30},
		{"thin", 2, 2.0, 2.0, 0.10, 0.10, 2, 2, 1},
	}
	for _, r := range rows {
		if _, err := db.Exec(insert, r...); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Run(context.Background(), db, Gates{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"holds": "HOLDS", "decays": "DECAYS", "curve": "CURVE_FIT", "thin": "INSUFFICIENT"}
	if len(got) != len(want) {
		t.Fatalf("got %d verdicts", len(got))
	}
	for _, v := range got {
		if want[v.StrategyID] != v.Verdict {
			t.Errorf("%s = %s, want %s", v.StrategyID, v.Verdict, want[v.StrategyID])
		}
	}
}

func TestOnlyKeepsListedStrategies(t *testing.T) {
	rows := []Verdict{{StrategyID: "a"}, {StrategyID: "B"}, {StrategyID: "c"}}
	if got := Only(rows, ""); len(got) != 3 {
		t.Errorf("empty keeps all, got %d", len(got))
	}
	if got := Only(rows, "all"); len(got) != 3 {
		t.Errorf("all keeps all, got %d", len(got))
	}
	got := Only(rows, "b, A")
	if len(got) != 2 || got[0].StrategyID != "a" || got[1].StrategyID != "B" {
		t.Errorf("got %+v", got)
	}
}

func TestVerdictCarriesOutOfSampleIdleDays(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE walk_forward_summary (
		strategy_id TEXT, folds INT, is_sharpe REAL, oos_sharpe REAL,
		is_return_pct REAL, oos_return_pct REAL, oos_trades INT,
		oos_positive_folds INT, trials INT, oos_idle_days INT, oos_days INT, oos_idle_pct REAL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO walk_forward_summary VALUES ('a', 4, 1.0, 0.8, 0.1, 0.08, 20, 3, 4, 30, 100, 0.3), ('old', 4, 1.0, 0.8, 0.1, 0.08, 20, 3, 4, NULL, NULL, NULL)`); err != nil {
		t.Fatal(err)
	}
	got, err := Run(context.Background(), db, Gates{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Verdict{}
	for _, v := range got {
		by[v.StrategyID] = v
	}
	if by["a"].OOSIdleDays != 30 || by["a"].OOSDays != 100 || by["old"].OOSDays != 0 {
		t.Fatalf("idle days not carried: %+v", by)
	}
	if out := Format(got); !strings.Contains(out, "30/100 (30%)") {
		t.Errorf("table should show idle days:\n%s", out)
	}
}
