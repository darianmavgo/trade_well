package appserve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/ready"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
)

// pagesRoot mounts the real operator pages on a real database in a temp folder, with the
// dashboard's credentials set, and returns the handler a browser would reach.
func pagesRoot(t *testing.T) http.Handler {
	t.Helper()
	app := t.TempDir()
	t.Setenv("APP_FOLDER", app)
	t.Setenv("ORCHESTRATOR_DB", filepath.Join(app, "orch.db"))
	t.Setenv("DASHBOARD_USERNAME", "u")
	t.Setenv("DASHBOARD_PASSWORD", "p")
	t.Setenv("STRATEGY_ALLOWLIST", "markov_model_mull+streak-shpu-down3+markov_model_amuu+markov_model_vrtl")
	t.Setenv("SCHWAB_TOKEN_PATH", filepath.Join(app, "token.json"))
	db, err := bootstrap.OpenDB(filepath.Join(app, "orch.db"))
	if err != nil {
		t.Fatal(err)
	}
	loc := job_runs.EasternTime()
	at := time.Now().In(loc).Add(-time.Hour)
	if _, err := job_runs.RecordJobRun(db, job_runs.JobRun{Job: "scan_market", Status: job_runs.JobRunFailed, Summary: "LIVESCAN_FAILED: period", StartedAt: at, FinishedAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := scan_market.UpsertSignalDay(db, "streak-shpu-down3", time.Now().UTC().Format("2006-01-02"), "", "NO_BUY", 0, "no buy signal from livescan", "NO_BUY", time.Now()); err != nil {
		t.Fatal(err)
	}
	// History of a strategy that was deployed before and no longer is.
	today := time.Now().UTC().Format("2006-01-02")
	if _, err := scan_market.UpsertSignalDay(db, "streak-voo-buy-tecl", today, "", "NO_BUY", 0, "an old strategy's note", "NO_BUY", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO scan_market_buy_signal (scan_run_id, stack_id, strategy_id, symbol, as_of_date, status, created_at) VALUES ('r', '', 'rotation-old', 'ZSL', ?, 'STAGED', '2026-10-12T22:00:00Z')`, today); err != nil {
		t.Fatal(err)
	}
	// The reasons the evening scan stored: one for the deployed strategy, one for an old one.
	for _, q := range []string{
		`INSERT INTO scan_market_trace (scan_run_id, strategy_id, symbol, session_date, is_asof, entry, reason, facts_json, created_at)
		 VALUES ('20261012T220000Z', 'streak-shpu-down3', 'SHPU', '` + today + `', 1, 0, 'no entry: SHPU down_streak is 0, needs 3', '{"streak":0,"needs":3,"close":23.2}', '` + today + `T22:00:00Z')`,
		`INSERT INTO scan_market_trace (scan_run_id, strategy_id, symbol, session_date, is_asof, entry, reason, facts_json, created_at)
		 VALUES ('20261012T220000Z', 'rotation-old', 'ZSL', '` + today + `', 1, 0, 'an old strategy reason', '', '` + today + `T22:00:00Z')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	root := http.NewServeMux()
	for _, register := range []func(*http.ServeMux) error{registerOps, registerReadyPage, registerScanPage, registerEnv} {
		if err := register(root); err != nil {
			t.Fatal(err)
		}
	}
	return withChrome(root)
}

func get(h http.Handler, path string, auth bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if auth {
		req.SetBasicAuth("u", "p")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestOperatorPagesAreHTMLByDefaultAndJSONOnRequest(t *testing.T) {
	h := pagesRoot(t)
	for _, path := range []string{"/ops", "/ready", "/scan", "/env"} {
		if rec := get(h, path, false); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without credentials = %d, want 401", path, rec.Code)
		}
		page := get(h, path, true)
		if page.Code != http.StatusOK || !strings.HasPrefix(page.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s default = %d %s, want 200 text/html", path, page.Code, page.Header().Get("Content-Type"))
		}
		body := page.Body.String()
		if !strings.Contains(body, `class="chrome-nav"`) || !strings.Contains(body, `class="chrome-foot"`) || !strings.Contains(body, "bundle ") {
			t.Errorf("%s lacks the navigation bar or the version footer", path)
		}
		js := get(h, path+"?format=json", true)
		if !strings.HasPrefix(js.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s?format=json content type %s", path, js.Header().Get("Content-Type"))
		}
		var v any
		if err := json.Unmarshal(js.Body.Bytes(), &v); err != nil {
			t.Errorf("%s?format=json is not JSON: %v", path, err)
		}
		if strings.Contains(js.Body.String(), "chrome-nav") {
			t.Errorf("%s?format=json must not carry page chrome", path)
		}
	}
}

func TestOpsShowsTheFailedScanAndOffersTypedConfirmationForLiveJobs(t *testing.T) {
	body := get(pagesRoot(t), "/ops", true).Body.String()
	for _, want := range []string{"scan_market", "LIVESCAN_FAILED", `name="nonce"`, `placeholder="type submit_orders"`, "Run LIVE", "Dry run"} {
		if !strings.Contains(body, want) {
			t.Errorf("/ops lacks %q", want)
		}
	}
}

func TestScanPageListsTheNoBuyReason(t *testing.T) {
	body := get(pagesRoot(t), "/scan", true).Body.String()
	if !strings.Contains(body, "no buy signal from livescan") || !strings.Contains(body, "streak-shpu-down3") {
		t.Errorf("/scan lacks the no-buy row:\n%s", body)
	}
}

func TestScanPageExplainsEachVerdict(t *testing.T) {
	h := pagesRoot(t)
	body := get(h, "/scan", true).Body.String()
	for _, want := range []string{"Why, per strategy", "no entry: SHPU down_streak is 0, needs 3", "needs=3"} {
		if !strings.Contains(body, want) {
			t.Errorf("/scan lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "an old strategy reason") {
		t.Error("/scan default shows an undeployed strategy's reason")
	}
	if js := get(h, "/scan?format=json", true).Body.String(); !strings.Contains(js, `"trace"`) || !strings.Contains(js, "down_streak is 0") {
		t.Errorf("JSON lacks the trace: %s", js)
	}
}

func TestScanPageShowsOnlyDeployedStrategiesUnlessAsked(t *testing.T) {
	h := pagesRoot(t)
	def := get(h, "/scan", true).Body.String()
	if !strings.Contains(def, "streak-shpu-down3") || strings.Contains(def, "rotation-old") || strings.Contains(def, "an old strategy") {
		t.Errorf("/scan default must show the deployed strategy and hide the old ones:\n%s", def)
	}
	if !strings.Contains(def, "show them") {
		t.Error("/scan default must say rows were hidden and offer ?all=1")
	}
	all := get(h, "/scan?all=1", true).Body.String()
	if !strings.Contains(all, "rotation-old") || !strings.Contains(all, "an old strategy") {
		t.Errorf("/scan?all=1 must include the old strategies:\n%s", all)
	}
	var v struct {
		Buys     []any    `json:"buys"`
		Hidden   int      `json:"hidden"`
		Deployed []string `json:"deployed"`
	}
	if err := json.Unmarshal(get(h, "/scan?format=json", true).Body.Bytes(), &v); err != nil || v.Buys == nil || v.Hidden != 2 || len(v.Deployed) != 4 {
		t.Errorf("json hidden=%d deployed=%v err=%v, want 2 hidden and 4 deployed", v.Hidden, v.Deployed, err)
	}
}

func TestReadyStrictReturns503WhenBad(t *testing.T) {
	h := pagesRoot(t)
	// No Schwab token file exists in the temp folder, so the readiness status is bad.
	if rec := get(h, "/ready?format=json&strict=1", true); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("strict /ready = %d, want 503 (no token file)", rec.Code)
	}
	if rec := get(h, "/ready?format=json", true); rec.Code != http.StatusOK {
		t.Errorf("non-strict /ready = %d, want 200", rec.Code)
	}
}

func TestOpsRunRefusesWithoutTokenConfirmationOrSameOrigin(t *testing.T) {
	h := pagesRoot(t)
	post := func(form url.Values, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/ops/run", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth("u", "p")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(url.Values{"job": {"ready"}, "mode": {"dry"}, "nonce": {"nope"}}, nil); rec.Code != http.StatusForbidden {
		t.Errorf("bad nonce = %d, want 403", rec.Code)
	}
	if rec := post(url.Values{"job": {"ready"}, "mode": {"dry"}, "nonce": {pageNonces.issue()}}, map[string]string{"Sec-Fetch-Site": "cross-site"}); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site = %d, want 403", rec.Code)
	}
	// A live job without its name typed is refused, and nothing runs.
	if rec := post(url.Values{"job": {"submit_orders"}, "mode": {"live"}, "confirm": {"yes"}, "nonce": {pageNonces.issue()}}, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("live without the typed name = %d, want 400", rec.Code)
	}
	if rec := post(url.Values{"job": {"submit_orders"}, "mode": {"run"}, "nonce": {pageNonces.issue()}}, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("one-click run of a live job = %d, want 400", rec.Code)
	}
	if rec := post(url.Values{"job": {"submit_orders"}, "mode": {"dry"}, "nonce": {pageNonces.issue()}}, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("dry run of a job with none = %d, want 400", rec.Code)
	}
	if rec := post(url.Values{"job": {"nope"}, "mode": {"run"}, "nonce": {pageNonces.issue()}}, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown job = %d, want 400", rec.Code)
	}
}

func TestOpsRunsAJobInProcessThroughTheCronHandler(t *testing.T) {
	h := pagesRoot(t)
	t.Setenv("EMAIL_RECIPIENT", "")
	form := url.Values{"job": {"ready"}, "mode": {"dry"}, "nonce": {pageNonces.issue()}}
	req := httptest.NewRequest(http.MethodPost, "/ops/run", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth("u", "p")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dry run of ready = %d: %s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "OK ready:") || !strings.Contains(body, "HTTP 200") {
		t.Errorf("result page lacks the job's outcome:\n%s", body)
	}
	// The run left a job_run row, like a scheduled run does.
	db, err := bootstrap.OpenDB(os.Getenv("ORCHESTRATOR_DB"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if last, err := job_runs.LastJobRun(db, "ready"); err != nil || last == nil || last.Status != job_runs.JobRunOK {
		t.Errorf("ready job_run = %+v, err %v", last, err)
	}
}

func TestEmailCSSBakesInTheLightPalette(t *testing.T) {
	css := emailCSS(dashboard.PageStyle() + pageExtraCSS)
	for _, bad := range []string{"var(--", "prefers-color-scheme", "#0f1216"} {
		if strings.Contains(css, bad) {
			t.Errorf("email css still contains %q", bad)
		}
	}
	for _, want := range []string{"background:#fff", "color:#1a7f37", "color:#c62828"} {
		if !strings.Contains(css, want) {
			t.Errorf("email css lacks %q", want)
		}
	}
}

func TestReadyEmailIsTheReadyPage(t *testing.T) {
	t.Setenv("ORCHESTRATOR_URL", "https://example.test")
	rep := ready.Report{Status: ready.Warn, GeneratedAt: time.Date(2026, 10, 15, 21, 30, 0, 0, time.UTC), Checks: []ready.Check{
		{Name: "markov_models", Status: ready.Warn, Detail: "MULL: scored through 2026-10-09 <behind>"},
		{Name: "killswitch", Status: ready.OK, Detail: "released"},
	}}
	body, err := readyEmailHTML(context.Background(), rep)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Ready: WARN", "markov_models", "tag warn", "tag ok", "&lt;behind&gt;", `href="https://example.test/ops"`, "<table>"} {
		if !strings.Contains(body, want) {
			t.Errorf("email lacks %q", want)
		}
	}
	if strings.Contains(body, "var(--") || strings.Contains(body, `href="/`) {
		t.Errorf("email still has CSS variables or relative links")
	}
}
