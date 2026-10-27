package appserve

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
)

func killswitchHandler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	if err := registerKillswitch(mux); err != nil {
		t.Fatal(err)
	}
	if err := registerE2E(mux); err != nil {
		t.Fatal(err)
	}
	return mux
}

var ksNonce = regexp.MustCompile(`name="nonce" value="([0-9a-f]+)"`)

func ksPageNonce(t *testing.T, h http.Handler) string {
	t.Helper()
	m := ksNonce.FindStringSubmatch(e2eDo(h, "GET", "/killswitch", nil, true, nil).Body.String())
	if m == nil {
		t.Fatal("no nonce on the killswitch page")
	}
	return m[1]
}

func engaged(t *testing.T, dbPath string) killswitch.State {
	t.Helper()
	db, err := bootstrap.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := killswitch.Current(db)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func cronGet(job string) (int, string) {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/cron/"+job, nil)
	req.Header.Set("X-Appengine-Cron", "true")
	w := &bufferedResp{h: http.Header{}, code: 200}
	CronHandler().ServeHTTP(w, req)
	return w.code, w.body.String()
}

type bufferedResp struct {
	h    http.Header
	code int
	body strings.Builder
}

func (b *bufferedResp) Header() http.Header         { return b.h }
func (b *bufferedResp) WriteHeader(c int)           { b.code = c }
func (b *bufferedResp) Write(p []byte) (int, error) { return b.body.Write(p) }

func TestKillswitchPageRequiresTheDashboardAuthAndGuardsItsPost(t *testing.T) {
	dbPath := e2eSafeEnv(t)
	h := killswitchHandler(t)
	if w := e2eDo(h, "GET", "/killswitch", nil, false, nil); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no credentials = %d", w.Code)
	}
	w := e2eDo(h, "GET", "/killswitch", nil, true, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ENGAGE") || !strings.Contains(w.Body.String(), "Killswitch is off") {
		t.Fatalf("page = %d\n%s", w.Code, w.Body.String())
	}
	cases := []struct {
		name string
		form url.Values
		hdr  map[string]string
		want int
	}{
		{"no token", url.Values{"action": {"engage"}}, nil, http.StatusForbidden},
		{"forged token", url.Values{"action": {"engage"}, "nonce": {"deadbeef"}}, nil, http.StatusForbidden},
		{"cross-site", url.Values{"action": {"engage"}, "nonce": {ksPageNonce(t, h)}}, map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"bad action", url.Values{"action": {"explode"}, "nonce": {ksPageNonce(t, h)}}, nil, http.StatusBadRequest},
	}
	for _, c := range cases {
		if got := e2eDo(h, "POST", "/killswitch", c.form, true, c.hdr); got.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, got.Code, c.want)
		}
	}
	if e2eDo(h, "POST", "/killswitch", url.Values{"action": {"engage"}}, false, nil).Code != http.StatusUnauthorized {
		t.Error("a POST without credentials must be 401")
	}
	if engaged(t, dbPath).Engaged {
		t.Error("a refused request engaged the switch")
	}
}

// Engage: cron is refused, running executions are cancelled, nothing is recorded as a
// job run; release: cron runs again. The whole flow through the real endpoint and the
// real cron handlers.
func TestKillswitchEngagesRefusesCronCancelsRunningJobsAndReleases(t *testing.T) {
	dbPath := e2eSafeEnv(t)
	h := killswitchHandler(t)

	// A job is "running": its context is registered like every cron route's is.
	runCtx, done := killswitch.Track(context.Background(), "scan_market")
	defer done()

	w := e2eDo(h, "POST", "/killswitch", url.Values{"action": {"engage"}, "reason": {"TSLL misbehaving"}, "nonce": {ksPageNonce(t, h)}}, true, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "ENGAGED") || !strings.Contains(w.Body.String(), "scan_market") {
		t.Fatalf("engage = %d\n%s", w.Code, w.Body.String())
	}
	st := engaged(t, dbPath)
	if !st.Engaged || st.ChangedBy != "admin" || st.Reason != "TSLL misbehaving" {
		t.Errorf("state = %+v", st)
	}
	select {
	case <-runCtx.Done():
		if !strings.Contains(context.Cause(runCtx).Error(), "killswitch") {
			t.Errorf("cause = %v", context.Cause(runCtx))
		}
	default:
		t.Error("the running job's context was not cancelled")
	}

	for _, job := range []string{"account_state", "scan_market", "stage_orders", "submit_orders", "manage_exits", "check_health"} {
		code, body := cronGet(job)
		if code != http.StatusServiceUnavailable || !strings.Contains(body, "ENGAGED") || !strings.Contains(body, "did not run") {
			t.Errorf("/cron/%s while engaged = %d %q, want 503 naming the killswitch", job, code, body)
		}
	}
	db, _ := bootstrap.OpenDB(dbPath)
	var runs int
	_ = db.QueryRow(`SELECT COUNT(*) FROM job_run`).Scan(&runs)
	db.Close()
	if runs != 0 {
		t.Errorf("a refused cron hit must not be recorded as a run, got %d job_run rows", runs)
	}

	// The page shows it, and the live e2e run (real orders) is refused too.
	if body := e2eDo(h, "GET", "/killswitch", nil, true, nil).Body.String(); !strings.Contains(body, "KILLSWITCH IS ENGAGED") || !strings.Contains(body, "Release") {
		t.Errorf("engaged page:\n%s", body)
	}
	nonce := pageNonce(t, h)
	if w := e2eDo(h, "POST", "/run_e2e_live", url.Values{"nonce": {nonce}}, true, nil); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "did not run") {
		t.Errorf("run_e2e_live while engaged = %d %q", w.Code, w.Body.String())
	}

	// Release: cron gets past the gate (the job itself then fails for lack of config,
	// which is not 503).
	w = e2eDo(h, "POST", "/killswitch", url.Values{"action": {"release"}, "nonce": {ksPageNonce(t, h)}}, true, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "RELEASED") {
		t.Fatalf("release = %d\n%s", w.Code, w.Body.String())
	}
	if engaged(t, dbPath).Engaged {
		t.Error("still engaged after release")
	}
	if code, _ := cronGet("manage_exits"); code == http.StatusServiceUnavailable {
		t.Error("cron must run again after release")
	}
}

// Engaging is idempotent and the token is single use.
func TestKillswitchTokenIsSingleUse(t *testing.T) {
	e2eSafeEnv(t)
	h := killswitchHandler(t)
	n := ksPageNonce(t, h)
	form := url.Values{"action": {"engage"}, "nonce": {n}}
	if w := e2eDo(h, "POST", "/killswitch", form, true, nil); w.Code != http.StatusOK {
		t.Fatalf("first use = %d", w.Code)
	}
	if w := e2eDo(h, "POST", "/killswitch", form, true, nil); w.Code != http.StatusForbidden {
		t.Errorf("reused token = %d, want 403", w.Code)
	}
}
