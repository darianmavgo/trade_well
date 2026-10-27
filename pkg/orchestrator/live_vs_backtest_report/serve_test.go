package live_vs_backtest_report

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_state"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
)

func TestPageRequiresAuthAndServesTheReport(t *testing.T) {
	t.Setenv("DASHBOARD_USERNAME", "admin")
	t.Setenv("DASHBOARD_PASSWORD", "s3cr3t")
	dir := t.TempDir()
	orch := filepath.Join(dir, "orchestrator.db")
	market := filepath.Join(dir, "market.db")
	db, err := sql.Open("sqlite", orch)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := manage_exits.EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	if err := account_state.MirrorStore().EnsureSchema(db); err != nil {
		t.Fatal(err)
	}
	seedReportBook(t, db)
	if err := seedMarket(market); err != nil {
		t.Fatal(err)
	}
	h, err := newHandler(&appconfig.Config{
		DBPath:            orch,
		MarketDB:          market,
		StrategyAllowlist: []string{"tsll-daily-one-share"},
	}, db, nil)
	if err != nil {
		t.Fatal(err)
	}

	anon := httptest.NewRequest(http.MethodGet, "/", nil)
	aw := httptest.NewRecorder()
	h.ServeHTTP(aw, anon)
	if aw.Code != http.StatusUnauthorized {
		t.Fatalf("anon status %d", aw.Code)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "s3cr3t")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if w.Header().Get("X-Reconcile-Report") != "OK, with warnings" {
		t.Fatalf("header %q", w.Header().Get("X-Reconcile-Report"))
	}
	if !strings.Contains(w.Body.String(), "tsll-daily-one-share") || !strings.Contains(w.Body.String(), "?refresh=1") || !strings.Contains(w.Body.String(), "live_vs_backtest_report OK, with warnings") {
		t.Fatalf("body missing report:\n%s", w.Body.String())
	}

	again := httptest.NewRequest(http.MethodGet, "/", nil)
	again.SetBasicAuth("admin", "s3cr3t")
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, again)
	if w2.Code != http.StatusOK || w2.Header().Get("X-Reconcile-Report") != "OK, with warnings" {
		t.Fatalf("cached status %d header %q", w2.Code, w2.Header().Get("X-Reconcile-Report"))
	}
}

func TestPageReportsAFailedRestore(t *testing.T) {
	t.Setenv("DASHBOARD_USERNAME", "admin")
	t.Setenv("DASHBOARD_PASSWORD", "s3cr3t")
	h, err := newHandler(&appconfig.Config{DBPath: filepath.Join(t.TempDir(), "orchestrator.db")}, nil, os.ErrNotExist)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetBasicAuth("admin", "s3cr3t")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusInternalServerError || w.Header().Get("X-Reconcile-Report") != "FAILED" {
		t.Fatalf("status %d header %q", w.Code, w.Header().Get("X-Reconcile-Report"))
	}
	if !strings.Contains(w.Body.String(), "live_vs_backtest_report FAILED") {
		t.Fatalf("body %s", w.Body.String())
	}
}

// TestReportLocalLauncherIsReadOnly pins the local report launcher to the
// no-args GAE entrypoint, its own /tmp folder, and no write mode.
func TestReportLocalLauncherIsReadOnly(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	scriptPath := filepath.Join(root, "deploy_report_local", "servelocal.sh")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("%s is not executable", scriptPath)
	}
	raw, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"--takeover", "serve -port", "DBSYNC_REPLICATE=1", "REPLICATE=1"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("deploy_report_local/servelocal.sh contains %q", forbidden)
		}
	}
	for _, need := range []string{
		"/tmp/trade_orchestrator_report",
		"deploy_gae/live_vs_backtest_report.yaml",
		"GAE_SERVICE=",
		"orchestrator.db",
	} {
		if !strings.Contains(text, need) {
			t.Fatalf("deploy_report_local/servelocal.sh missing %q", need)
		}
	}
	yamlRaw, err := os.ReadFile(filepath.Join(root, "deploy_gae", "live_vs_backtest_report.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(yamlRaw), "service: "+ServiceName) {
		t.Fatalf("deploy_gae/live_vs_backtest_report.yaml service drifted from %s", ServiceName)
	}
}
