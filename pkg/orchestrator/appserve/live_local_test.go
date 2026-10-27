package appserve

import (
	"database/sql"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	_ "modernc.org/sqlite"
)

// TestLiveLocalScan checks, against a running deploy_live_local/servelocal.sh, that the
// strategies in the live allowlist are greenlit and that the scan route evaluates them.
// It talks to the real server over HTTP: no mocks, no fakes.
//
//	./deploy_live_local/servelocal.sh                  # in another terminal
//	LIVE_LOCAL=1 DASHBOARD_USERNAME=... DASHBOARD_PASSWORD=... \
//	  go test ./pkg/appserve -run TestLiveLocalScan -v -timeout 20m
//
// Optional: LIVE_LOCAL_URL (default http://localhost:8082), LIVE_LOCAL_EXPECT (comma-separated
// strategy ids that must be greenlit; default the clean stack), LIVE_LOCAL_APP_FOLDER (default
// /tmp/trade_orchestrator, where the server keeps its trained models).
//
// The scan runs with ?dry_run=1, so it sends no email. It does download bars and train the
// markov models into the local mirror's folder; nothing is pushed to GCS or Schwab.
func TestLiveLocalScan(t *testing.T) {
	if os.Getenv("LIVE_LOCAL") != "1" {
		t.Skip("set LIVE_LOCAL=1 with servelocal.sh running on :8082")
	}
	base := envOr("LIVE_LOCAL_URL", "http://localhost:8082")
	user, pass := os.Getenv("DASHBOARD_USERNAME"), os.Getenv("DASHBOARD_PASSWORD")
	if user == "" || pass == "" {
		t.Fatal("set DASHBOARD_USERNAME and DASHBOARD_PASSWORD (the dashboard login in the live config)")
	}
	expect := strings.Split(envOr("LIVE_LOCAL_EXPECT",
		"streak-voo-buy-tecl,markov_model_amd,markov_model_lite,markov_model_mull"), ",")
	client := &http.Client{Timeout: 15 * time.Minute}
	get := func(path string, hdr map[string]string) (int, string) {
		req, err := http.NewRequest(http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.SetBasicAuth(user, pass)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v (is servelocal.sh running?)", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// 1. The strategies are greenlit in the config the server loaded.
	code, page := get("/env", nil)
	if code != 200 {
		t.Fatalf("/env: HTTP %d", code)
	}
	m := regexp.MustCompile(`<th>STRATEGY_ALLOWLIST</th><td[^>]*>([^<]*)</td>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("/env shows no STRATEGY_ALLOWLIST")
	}
	allowlist := html.UnescapeString(m[1])
	t.Logf("STRATEGY_ALLOWLIST = %s", allowlist)
	greenlit := map[string]bool{}
	for _, entry := range strings.Split(allowlist, ",") {
		for _, id := range strategy.ParseStack(strings.TrimSpace(entry)) {
			greenlit[id] = true
		}
	}
	for _, id := range expect {
		if !greenlit[id] {
			t.Errorf("%s is not greenlit: set it with deploy_gae/config.sh set STRATEGY_ALLOWLIST=... and restart servelocal", id)
		}
	}
	if t.Failed() {
		return
	}

	// 2. The scan route resolves and evaluates them. App Engine's cron header is what
	// the route admits; sending it here is the local equivalent of a cron call.
	code, body := get("/cron/scan_market?dry_run=1", map[string]string{"X-Appengine-Cron": "true"})
	t.Logf("scan_market: HTTP %d %s", code, strings.TrimSpace(body))
	if code != 200 || !strings.HasPrefix(body, "OK scan_market") {
		t.Fatalf("scan_market failed: HTTP %d %s", code, body)
	}
	field := func(name string) string {
		f := regexp.MustCompile(`\b` + name + `=(\S+)`).FindStringSubmatch(body)
		if f == nil {
			t.Fatalf("scan_market reply has no %s=: %s", name, body)
		}
		return f[1]
	}
	asOf := field("as_of")
	day, err := time.Parse("2006-01-02", asOf)
	if err != nil {
		t.Fatalf("as_of %q: %v", asOf, err)
	}
	if age := time.Since(day); age > 5*24*time.Hour {
		t.Errorf("as_of %s is %v old: the bars were not refreshed", asOf, age.Round(time.Hour))
	}
	if n, _ := strconv.Atoi(field("members")); n < len(expect) {
		t.Errorf("scan evaluated %d members, want at least %d", n, len(expect))
	}

	// 3. Each markov strategy's model was trained through the scan's as-of date.
	models := filepath.Join(envOr("LIVE_LOCAL_APP_FOLDER", "/tmp/trade_orchestrator"), "data", "markov_models.db")
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(models)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, id := range expect {
		sym, ok := strings.CutPrefix(id, "markov_model_")
		if !ok {
			continue
		}
		var last string
		sym = strings.ToUpper(sym)
		if err := db.QueryRow(`SELECT last_date FROM markov_model_meta WHERE symbol = ?`, sym).Scan(&last); err != nil {
			t.Errorf("%s: no trained model in %s: %v", id, models, err)
		} else if last != asOf {
			t.Errorf("%s: model trained through %s, want the scan's as-of %s", id, last, asOf)
		}
	}
	fmt.Fprintf(os.Stderr, "live_local scan ok: as_of=%s %d strategies greenlit\n", asOf, len(greenlit))
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
