package weekly_digest

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/ready"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
)

func TestDigestSummarisesTheWeekFromTheDatabase(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	db, err := bootstrap.OpenDB(filepath.Join(t.TempDir(), "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 10, 17, 9, 0, 0, 0, job_runs.EasternTime()) // Saturday
	rec := func(job, status, sum string, daysAgo int) {
		at := now.AddDate(0, 0, -daysAgo)
		if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{Job: job, Status: status, Summary: sum, StartedAt: at, FinishedAt: at.Add(time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	rec("scan_market", "OK", "OK", 1)
	rec("scan_market", "FAILED", "LIVESCAN_FAILED: period", 3)
	rec("scan_market", "OK", "old", 20) // outside the week
	if _, err := scan_market.UpsertSignalDay(db, "streak-shpu-down3", "2026-10-16", "", "NO_BUY", 0, "no buy", "NO_BUY", now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO account_state_position (account_number, symbol, long_quantity, average_price, market_value, fetched_at, exit_date)
		VALUES ('12345678', 'MULL', 10, 20, 250, '2026-10-16T20:00:00Z', '2026-10-30')`); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	d, err := RunJob(context.Background(), db, &out, JobOptions{Ready: ready.Options{Now: now, Static: true, Allowlist: []string{"streak-shpu-down3"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Jobs) != 1 || d.Jobs[0].OK != 1 || d.Jobs[0].Failed != 1 || d.NoBuys != 1 {
		t.Fatalf("digest %+v", d)
	}
	if len(d.Positions) != 1 || d.Positions[0].Unrealized != 50 {
		t.Fatalf("positions %+v, want one with +50 unrealized", d.Positions)
	}
	text := out.String()
	if first, _, _ := strings.Cut(text, "\n"); !strings.HasPrefix(first, "OK weekly_digest 2026-") {
		t.Fatalf("first line %q", first)
	}
	if strings.Contains(text, "12345678") || !strings.Contains(text, "…5678") {
		t.Fatalf("account number not masked:\n%s", text)
	}
}

func TestGroupBuysFoldsAStrategyThatFiresOnManySymbols(t *testing.T) {
	var buys []scan_market.BuyRow
	for _, sym := range []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J"} {
		buys = append(buys, scan_market.BuyRow{AsOf: "2026-10-09", StrategyID: "rotation", Status: "STAGED", Symbol: sym})
	}
	buys = append(buys, scan_market.BuyRow{AsOf: "2026-10-09", StrategyID: "markov_model_mull", Status: "NEW", Symbol: "MULL"})
	g := groupBuys(buys)
	if len(g) != 2 || len(g[0].Symbols) != 10 || g[1].StrategyID != "markov_model_mull" {
		t.Fatalf("groups %+v", g)
	}
	if got := symbolList(g[0].Symbols); got != "A B C D E F G H … +2 more" {
		t.Fatalf("symbolList = %q", got)
	}
}
