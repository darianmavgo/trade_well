package strategies_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

const cleanStack = "streak-voo-buy-tecl+markov_model_amd+markov_model_lite+markov_model_mull"

// With no reference database in the app folder, the bundled one makes every row of the
// stack resolvable.
func TestBundledRowsResolveTheCleanStack(t *testing.T) {
	app := t.TempDir()
	t.Setenv("APP_FOLDER", app)
	got, err := strategies.ResolveGreenlit(nil, []string{cleanStack})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range got {
		ids = append(ids, s.ID())
	}
	want := strings.Split(cleanStack, "+")
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("resolved %v, want %v", ids, want)
	}
	if _, err := os.Stat(filepath.Join(app, "refdata", "strategies.db")); err != nil {
		t.Fatalf("the bundled database was not placed: %v", err)
	}
}

// The stack in STRATEGY_ALLOWLIST (deploy_gae/config.sh list) resolves from the bundle, SHPU row included.
func TestBundledRowsResolveTheLiveStack(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	const live = "markov_model_mull+streak-shpu-down3+markov_model_amuu+markov_model_vrtl"
	got, err := strategies.ResolveGreenlit(nil, []string{live})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("resolved %d strategies, want 4", len(got))
	}
}

// A strategy that is not a bundled row fails loudly and says how to fix it.
func TestAnUnbundledRowFailsWithTheFix(t *testing.T) {
	t.Setenv("APP_FOLDER", t.TempDir())
	_, err := strategies.ResolveGreenlit(nil, []string{"mara_tree"})
	if err == nil || !strings.Contains(err.Error(), "strategy export") {
		t.Fatalf("got %v, want an error that names `strategy export`", err)
	}
}

// The scan the cron route runs, on the real stack: it downloads the symbols' bars, trains the markov
// models through the latest bar, and scans. It needs the network, so it only runs with
// STACK_SCAN_LIVE=1. It never touches Schwab and sends no email.
func TestCleanStackScansEndToEnd(t *testing.T) {
	if os.Getenv("STACK_SCAN_LIVE") != "1" {
		t.Skip("set STACK_SCAN_LIVE=1 to download bars and scan the stack")
	}
	app := t.TempDir()
	t.Setenv("APP_FOLDER", app)
	members, err := strategies.ResolveGreenlit(nil, []string{cleanStack})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]strategy.Strategy{}
	for _, s := range members {
		byID[s.ID()] = s
	}
	db, err := appconfig.OpenDB(filepath.Join(app, "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cfg := &appconfig.Config{DBPath: filepath.Join(app, "orch.db"), MarketDB: filepath.Join(app, "data", "market_history.db")}
	if err := os.MkdirAll(filepath.Dir(cfg.MarketDB), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := scan_market.RunSignalScan(context.Background(), db, cfg, []string{cleanStack}, byID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if res.MembersEvaluated != 4 || res.AsOf == "" {
		t.Fatalf("members %d as_of %q", res.MembersEvaluated, res.AsOf)
	}
	models, err := sql.Open("sqlite", filepath.Join(app, "data", "markov_models.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer models.Close()
	rows, err := models.Query(`SELECT symbol, last_date FROM markov_model_meta ORDER BY symbol`)
	if err != nil {
		t.Fatalf("the markov models were not trained during the scan: %v", err)
	}
	defer rows.Close()
	trained := map[string]string{}
	for rows.Next() {
		var sym, last string
		if err := rows.Scan(&sym, &last); err != nil {
			t.Fatal(err)
		}
		trained[sym] = last
	}
	for _, sym := range []string{"AMD", "LITE", "MULL"} {
		if trained[sym] != res.AsOf {
			t.Errorf("%s model trained through %q, want the scan's as-of date %s", sym, trained[sym], res.AsOf)
		}
	}
	t.Logf("as_of %s next %s, %d signal(s)", res.AsOf, res.NextSession, res.LivescanSignals)
	for _, d := range res.Details {
		t.Log(d)
	}
}
