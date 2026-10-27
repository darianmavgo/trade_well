package appserve

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
)

// e2eSafeEnv points the app at an empty temp database and removes everything a
// live run would need: no Schwab keys or token, no accounts, no strategies, no
// SMTP. Every step of a pipeline run then fails before it can touch an order or
// send an email, so the tests can drive the real endpoint and the real cron
// handlers.
func e2eSafeEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "orchestrator.db")
	for k, v := range map[string]string{
		"APP_FOLDER": dir, "ORCHESTRATOR_DB": dbPath,
		"DASHBOARD_USERNAME": "admin", "DASHBOARD_PASSWORD": "s3cr3t",
		"SCHWAB_APP_KEY": "", "SCHWAB_APP_SECRET": "",
		"SCHWAB_TOKEN_PATH":      filepath.Join(dir, "token.json"),
		"SCHWAB_ACCOUNT_NUMBERS": "", "STRATEGY_ALLOWLIST": "",
		"SMTP_HOST": "", "SMTP_USER": "", "SMTP_PASS": "", "EMAIL_RECIPIENT": "",
		"GCS_BUCKET": "", "CRON_ALLOWED_EMAILS": "",
	} {
		t.Setenv(k, v)
	}
	return dbPath
}

func e2eHandler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	if err := registerE2E(mux); err != nil {
		t.Fatal(err)
	}
	return mux
}

func e2eDo(h http.Handler, method, path string, form url.Values, auth bool, headers map[string]string) *httptest.ResponseRecorder {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if auth {
		req.SetBasicAuth("admin", "s3cr3t")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

var nonceRe = regexp.MustCompile(`name="nonce" value="([0-9a-f]+)"`)

func pageNonce(t *testing.T, h http.Handler) string {
	t.Helper()
	w := e2eDo(h, "GET", "/run_e2e_live", nil, true, nil)
	m := nonceRe.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatalf("no nonce on the page:\n%s", w.Body.String())
	}
	return m[1]
}

func e2eRuns(t *testing.T, dbPath string) int {
	t.Helper()
	db, err := bootstrap.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM job_run WHERE job = 'run_e2e_live'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Same Basic Auth as the dashboard, on the page and on the POST.
func TestRunE2ELiveRequiresTheDashboardAuth(t *testing.T) {
	e2eSafeEnv(t)
	h := e2eHandler(t)
	if w := e2eDo(h, "GET", "/run_e2e_live", nil, false, nil); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("GET without credentials = %d, want 401 with a Basic challenge", w.Code)
	}
	if w := e2eDo(h, "POST", "/run_e2e_live", url.Values{"nonce": {"x"}}, false, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("POST without credentials = %d, want 401", w.Code)
	}
}

// Loading the page places no orders and runs nothing.
func TestRunE2ELivePageOnlyOffersTheRun(t *testing.T) {
	dbPath := e2eSafeEnv(t)
	h := e2eHandler(t)
	w := e2eDo(h, "GET", "/run_e2e_live", nil, true, nil)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("GET = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
	body := w.Body.String()
	for _, want := range []string{"LIVE run", "real orders", `type="hidden" name="nonce"`, `method="post"`, "Run the live pipeline"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if pageNonce(t, h) == pageNonce(t, h) {
		t.Error("every page load must get a fresh token")
	}
	if n := e2eRuns(t, dbPath); n != 0 {
		t.Errorf("loading the page recorded %d run(s); it must run nothing", n)
	}
}

// The guards in front of a live run: each one refuses, and none of them runs it.
func TestRunE2ELiveRefusesWithoutTokenOrSameOrigin(t *testing.T) {
	dbPath := e2eSafeEnv(t)
	h := e2eHandler(t)

	cases := []struct {
		name    string
		form    func() url.Values
		headers map[string]string
		want    int
	}{
		{"no token", func() url.Values { return url.Values{} }, nil, http.StatusForbidden},
		{"forged token", func() url.Values { return url.Values{"nonce": {"deadbeef"}} }, nil, http.StatusForbidden},
		{"cross-site (Sec-Fetch-Site)", func() url.Values { return url.Values{"nonce": {pageNonce(t, h)}} },
			map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"other origin", func() url.Values { return url.Values{"nonce": {pageNonce(t, h)}} },
			map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
	}
	for _, c := range cases {
		if w := e2eDo(h, "POST", "/run_e2e_live", c.form(), true, c.headers); w.Code != c.want {
			t.Errorf("%s: got %d, want %d\n%s", c.name, w.Code, c.want, w.Body.String())
		}
	}
	if n := e2eRuns(t, dbPath); n != 0 {
		t.Errorf("a refused request started %d run(s)", n)
	}
	if w := e2eDo(h, "PUT", "/run_e2e_live", nil, true, nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT = %d, want 405", w.Code)
	}
}

// A request the guards refuse does not burn the token: a valid one still works.
func TestRunE2ELiveRefusedRequestKeepsTheToken(t *testing.T) {
	e2eSafeEnv(t)
	h := e2eHandler(t)
	n := pageNonce(t, h)
	if w := e2eDo(h, "POST", "/run_e2e_live", url.Values{"nonce": {n}}, true, map[string]string{"Sec-Fetch-Site": "cross-site"}); w.Code != http.StatusForbidden {
		t.Fatalf("got %d", w.Code)
	}
	if !pageNonces.valid(n) {
		t.Error("a refused cross-site request must not use up the token")
	}
}

// A full authorised run through the real endpoint and the real cron handlers.
// The environment has nothing a live run needs, so every step fails safely; what
// is under test is that the run happens, is narrated as it goes, is recorded, and
// that its token is single-use.
func TestRunE2ELiveRunsStreamsAndTheTokenIsSingleUse(t *testing.T) {
	dbPath := e2eSafeEnv(t)
	h := e2eHandler(t)
	n := pageNonce(t, h)
	form := url.Values{"nonce": {n}}

	w := e2eDo(h, "POST", "/run_e2e_live", form, true, map[string]string{"Sec-Fetch-Site": "same-origin"})
	if w.Code != http.StatusOK {
		t.Fatalf("authorised run = %d\n%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"<pre>", "STEP 1", "STEP 4", "STEP 8", "again to confirm it is idempotent", "SUMMARY", "check(s) FAILED", "</pre>"} {
		if !strings.Contains(body, want) {
			t.Errorf("output missing %q", want)
		}
	}
	if strings.Contains(body, "All checks PASSED") {
		t.Error("a pipeline with nothing configured must not pass")
	}
	if got := e2eRuns(t, dbPath); got != 1 {
		t.Fatalf("job_run rows for run_e2e_live = %d, want 1", got)
	}
	db, _ := bootstrap.OpenDB(dbPath)
	defer db.Close()
	last, err := job_runs.LastJobRun(db, "run_e2e_live")
	if err != nil || last == nil || last.Status != job_runs.JobRunFailed {
		t.Errorf("run should be recorded FAILED, got %+v (err %v)", last, err)
	}

	// Same token again: refused, nothing runs a second time.
	if w := e2eDo(h, "POST", "/run_e2e_live", form, true, nil); w.Code != http.StatusForbidden {
		t.Errorf("reused token = %d, want 403", w.Code)
	}
	if got := e2eRuns(t, dbPath); got != 1 {
		t.Errorf("a reused token started another run (rows=%d)", got)
	}
}

func TestSameOrigin(t *testing.T) {
	mk := func(host string, hdr map[string]string) *http.Request {
		r := httptest.NewRequest("POST", "/run_e2e_live", nil)
		r.Host = host
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return r
	}
	cases := []struct {
		name string
		r    *http.Request
		want bool
	}{
		{"no headers (curl, CLI)", mk("app.example", nil), true},
		{"same-origin", mk("app.example", map[string]string{"Sec-Fetch-Site": "same-origin"}), true},
		{"user typed the URL", mk("app.example", map[string]string{"Sec-Fetch-Site": "none"}), true},
		{"matching Origin", mk("app.example", map[string]string{"Origin": "https://app.example"}), true},
		{"cross-site", mk("app.example", map[string]string{"Sec-Fetch-Site": "cross-site"}), false},
		{"same-site other subdomain", mk("app.example", map[string]string{"Sec-Fetch-Site": "same-site"}), false},
		{"different Origin host", mk("app.example", map[string]string{"Origin": "https://evil.example"}), false},
	}
	for _, c := range cases {
		if got := sameOrigin(c.r); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
