package appserve

import (
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

func exitMarketHandler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	if err := registerExitMarket(mux); err != nil {
		t.Fatal(err)
	}
	registerCron(mux)
	return mux
}

var emNonce = regexp.MustCompile(`name="nonce" value="([0-9a-f]+)"`)

func emPageNonce(t *testing.T, h http.Handler) string {
	t.Helper()
	m := emNonce.FindStringSubmatch(e2eDo(h, "GET", "/exit_market", nil, true, nil).Body.String())
	if m == nil {
		t.Fatal("no nonce on the exit_market page")
	}
	return m[1]
}

func TestExitMarketRequiresTheDashboardAuthAndOnlyOffersTheAction(t *testing.T) {
	e2eSafeEnv(t)
	h := exitMarketHandler(t)
	if w := e2eDo(h, "GET", "/exit_market", nil, false, nil); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
		t.Errorf("no credentials = %d", w.Code)
	}
	w := e2eDo(h, "GET", "/exit_market", nil, true, nil)
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("page = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, want := range []string{"SELL EVERYTHING AT MARKET", "Preview", `name="mode" value="dry"`, `name="mode" value="live"`, `name="killswitch"`, "Nothing runs when this page loads"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if emPageNonce(t, h) == emPageNonce(t, h) {
		t.Error("every page load must get a fresh token")
	}
}

func TestExitMarketPostGuards(t *testing.T) {
	e2eSafeEnv(t)
	h := exitMarketHandler(t)
	cases := []struct {
		name string
		form func() url.Values
		hdr  map[string]string
		auth bool
		want int
	}{
		{"no credentials", func() url.Values { return url.Values{"mode": {"live"}, "nonce": {"x"}} }, nil, false, http.StatusUnauthorized},
		{"no token", func() url.Values { return url.Values{"mode": {"live"}} }, nil, true, http.StatusForbidden},
		{"forged token", func() url.Values { return url.Values{"mode": {"live"}, "nonce": {"deadbeef"}} }, nil, true, http.StatusForbidden},
		{"cross-site", func() url.Values { return url.Values{"mode": {"live"}, "nonce": {emPageNonce(t, h)}} }, map[string]string{"Sec-Fetch-Site": "cross-site"}, true, http.StatusForbidden},
		{"other origin", func() url.Values { return url.Values{"mode": {"live"}, "nonce": {emPageNonce(t, h)}} }, map[string]string{"Origin": "https://evil.example"}, true, http.StatusForbidden},
		{"no mode: never guess live", func() url.Values { return url.Values{"nonce": {emPageNonce(t, h)}} }, nil, true, http.StatusBadRequest},
		{"unknown mode", func() url.Values { return url.Values{"mode": {"maybe"}, "nonce": {emPageNonce(t, h)}} }, nil, true, http.StatusBadRequest},
	}
	for _, c := range cases {
		if w := e2eDo(h, "POST", "/exit_market", c.form(), c.auth, c.hdr); w.Code != c.want {
			t.Errorf("%s: %d, want %d\n%s", c.name, w.Code, c.want, w.Body.String())
		}
	}
	if w := e2eDo(h, "PUT", "/exit_market", nil, true, nil); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT = %d", w.Code)
	}
}

// An authorised, valid request in an environment with no broker or accounts gets as far
// as resolving the managed accounts and stops there: nothing can be sold, and the token
// is used up.
func TestExitMarketStopsBeforeAnythingHappensWhenTheBrokerIsUnreachable(t *testing.T) {
	e2eSafeEnv(t)
	h := exitMarketHandler(t)
	n := emPageNonce(t, h)
	form := url.Values{"mode": {"live"}, "nonce": {n}, "killswitch": {"1"}}
	w := e2eDo(h, "POST", "/exit_market", form, true, map[string]string{"Sec-Fetch-Site": "same-origin"})
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "cannot reach the broker or resolve the managed accounts") {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
	if w := e2eDo(h, "POST", "/exit_market", form, true, nil); w.Code != http.StatusForbidden {
		t.Errorf("reused token = %d, want 403", w.Code)
	}
}

// exit_market and killswitch are operator tools. Neither may ever be a scheduled or
// triggerable cron job: not a /cron route, not in cronJobs, not in cron.yaml.
func TestOperatorToolsAreNotCronJobs(t *testing.T) {
	for _, name := range CronJobNames() {
		if name == "exit_market" || name == "killswitch" || name == "run_e2e_live" || name == "clear_stale" {
			t.Errorf("%s must not be a cron job", name)
		}
	}
	e2eSafeEnv(t)
	h := exitMarketHandler(t)
	for _, name := range []string{"exit_market", "killswitch", "run_e2e_live", "clear_stale"} {
		req, _ := http.NewRequest("GET", "/cron/"+name, nil)
		req.Header.Set("X-Appengine-Cron", "true")
		w := &bufferedResp{h: http.Header{}, code: 200}
		h.ServeHTTP(w, req)
		if w.code != http.StatusNotFound {
			t.Errorf("/cron/%s = %d, want 404: it must not exist", name, w.code)
		}
	}
	yaml, err := os.ReadFile("../../../deploy_gae/cron.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"exit_market", "killswitch", "run_e2e_live", "clear_stale"} {
		if strings.Contains(string(yaml), name) {
			t.Errorf("cron.yaml schedules %s", name)
		}
	}
}
