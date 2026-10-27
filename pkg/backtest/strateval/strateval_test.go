package strateval

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSplitDatesNoLeak(t *testing.T) {
	dates := []string{
		"2020-01-02", "2021-01-04", "2022-01-03",
		"2023-01-03", "2024-06-03", "2024-12-31",
		"2025-01-02", "2025-06-02", "2025-12-31",
	}
	sp, err := SplitDates(dates, 12)
	if err != nil {
		t.Fatal(err)
	}
	if sp.ISEnd >= sp.OOSStart {
		t.Fatalf("ISEnd %s >= OOSStart %s", sp.ISEnd, sp.OOSStart)
	}
	for _, d := range FilterDatesInclusive(dates, sp.ISStart, sp.ISEnd) {
		if d >= sp.OOSStart {
			t.Fatalf("IS leak %s", d)
		}
	}
}

func TestAssignTier(t *testing.T) {
	g := DefaultGates()
	is := Metrics{MaxDD: 0.10, Sharpe: 1, Trades: 100, AvgTradePct: 0.01, WinRate: 0.6}
	// Monthly-ish: 12 trades, high win rate, contained DD → A
	oos := Metrics{MaxDD: 0.08, Sharpe: 0.5, Trades: 12, AvgTradePct: 0.01, WinRate: 0.60}
	tier, reasons := AssignTier(is, oos, g, false)
	if tier != "A" {
		t.Fatalf("want A got %s reasons=%v", tier, reasons)
	}
	// High DD / low win rate on allowlist → D
	bad := Metrics{MaxDD: 0.40, Sharpe: -0.2, Trades: 5, AvgTradePct: -0.01, WinRate: 0.30}
	tier, _ = AssignTier(is, bad, g, true)
	if tier != "D" {
		t.Fatalf("want D got %s", tier)
	}
	// Few trades but strong win rate / DD → still can be B (marginal), not C
	thin := Metrics{MaxDD: 0.05, Sharpe: 1.5, Trades: 8, AvgTradePct: 0.02, WinRate: 0.55}
	tier, _ = AssignTier(is, thin, g, false)
	if tier != "B" && tier != "A" {
		t.Fatalf("want B or A for thin high-quality, got %s", tier)
	}
}

func TestStoreAndAllowlistDiff(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "e.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	row := EvalRow{
		RunID: "r1", StrategyID: "demo_a", ParamsJSON: "{}",
		Split: Split{ISStart: "a", ISEnd: "b", OOSStart: "c", OOSEnd: "d"},
		IS:    Metrics{Sharpe: 1}, OOS: Metrics{Sharpe: 0.8, Trades: 40, AvgTradePct: 0.01, MaxDD: 0.1},
		Tier: "A", Reasons: []string{"ok"}, CreatedAt: time.Now().UTC(),
	}
	if err := s.Insert(row); err != nil {
		t.Fatal(err)
	}
	row.StrategyID = "live_bad"
	row.Tier = "D"
	if err := s.Insert(row); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestByStrategy("r1")
	if err != nil || len(got) != 2 {
		t.Fatalf("got=%d err=%v", len(got), err)
	}
	add, demote := AllowlistDiff(got, []string{"live_bad"})
	if len(add) != 1 || add[0] != "demo_a" {
		t.Fatalf("add=%v", add)
	}
	if len(demote) != 1 || demote[0] != "live_bad" {
		t.Fatalf("demote=%v", demote)
	}
}

func TestSyncAllowlistAndStatus(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	row := EvalRow{
		RunID: "r1", StrategyID: "alpha", ParamsJSON: "{}",
		Split: Split{ISStart: "a", ISEnd: "b", OOSStart: "c", OOSEnd: "d"},
		IS:    Metrics{Sharpe: 1}, OOS: Metrics{Sharpe: 0.9, Trades: 40, AvgTradePct: 0.01, MaxDD: 0.1},
		Tier: "A", Reasons: []string{"ok"}, CreatedAt: time.Now().UTC(),
	}
	if err := s.Insert(row); err != nil {
		t.Fatal(err)
	}
	row.StrategyID = "beta"
	row.Tier = "C"
	row.OOS = Metrics{Sharpe: -0.5, Trades: 10, AvgTradePct: -0.01, MaxDD: 0.3}
	if err := s.Insert(row); err != nil {
		t.Fatal(err)
	}

	live, retired, err := s.SyncAllowlist([]string{"beta", "gamma+overlay"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if live < 2 {
		t.Fatalf("live=%d", live)
	}
	_ = retired

	st, err := s.ListStatus()
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]StrategyStatus{}
	for _, x := range st {
		by[x.StrategyID] = x
	}
	if !by["beta"].Deployed || by["beta"].Lifecycle != "deployed_review" {
		t.Fatalf("beta=%+v", by["beta"])
	}
	if by["alpha"].Lifecycle != "scored_A" {
		t.Fatalf("alpha=%+v", by["alpha"])
	}
	if !by["gamma"].Deployed {
		t.Fatalf("gamma not deployed: %+v", by["gamma"])
	}

	_, retired, err = s.SyncAllowlist([]string{"alpha"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	if retired < 1 {
		t.Fatalf("expected retirements, got %d", retired)
	}
}

func TestIdleDaysAreStoredAndReported(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "idle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	row := EvalRow{
		RunID: "r1", StrategyID: "idle_a", ParamsJSON: "{}",
		Split: Split{ISStart: "a", ISEnd: "b", OOSStart: "c", OOSEnd: "d"},
		IS:    Metrics{Sharpe: 1, IdleDays: 600, Days: 1000}, OOS: Metrics{Sharpe: 0.8, Trades: 40, IdleDays: 135, Days: 250},
		Tier: "A", CreatedAt: time.Now().UTC(),
	}
	if err := s.Insert(row); err != nil {
		t.Fatal(err)
	}
	got, err := s.LatestByStrategy("r1")
	if err != nil || len(got) != 1 {
		t.Fatalf("got=%d err=%v", len(got), err)
	}
	if got[0].IS.IdleDays != 600 || got[0].IS.Days != 1000 || got[0].OOS.IdleDays != 135 || got[0].OOS.Days != 250 {
		t.Fatalf("idle days did not round trip: IS %+v OOS %+v", got[0].IS, got[0].OOS)
	}
	if out := FormatReport(got, nil); !strings.Contains(out, "135/250 (54%)") {
		t.Errorf("scorecard should show out-of-sample idle days:\n%s", out)
	}
}
