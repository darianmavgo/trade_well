package appserve

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/clear_stale"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/schemas"
)

var csNonce = regexp.MustCompile(`name="nonce" value="([0-9a-f]+)"`)

func clearStaleHandler(t *testing.T) (http.Handler, *sql.DB) {
	t.Helper()
	dbPath := e2eSafeEnv(t)
	db, err := appconfig.OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := schemas.EnsureAll(db); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STRATEGY_ALLOWLIST", "live")
	if _, err := db.Exec(`INSERT INTO stage_order (account_number, strategy_name, symbol, quantity, entry_price, take_profit_price, stop_loss_price, status, reject_reason, staged_at)
		VALUES ('111', 'retired', 'AGQ', 1, 10, 11, 9, 'REJECTED', 'status 429', ?)`, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	if err := registerClearStale(mux); err != nil {
		t.Fatal(err)
	}
	registerCron(mux)
	return mux, db
}

func csPageNonce(t *testing.T, h http.Handler) string {
	t.Helper()
	m := csNonce.FindStringSubmatch(e2eDo(h, "GET", "/clear_stale", nil, true, nil).Body.String())
	if m == nil {
		t.Fatal("no nonce on the clear_stale page")
	}
	return m[1]
}

func TestClearStalePageIsHTMLByDefaultAndJSONOnRequest(t *testing.T) {
	h, db := clearStaleHandler(t)
	if w := e2eDo(h, "GET", "/clear_stale", nil, false, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no credentials = %d", w.Code)
	}
	w := e2eDo(h, "GET", "/clear_stale", nil, true, nil)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("page = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if body := w.Body.String(); !strings.Contains(body, "AGQ") || !strings.Contains(body, "CLEAR THESE ROWS") {
		t.Errorf("page does not list the stale order and the action:\n%s", body)
	}
	w = e2eDo(h, "GET", "/clear_stale?format=json", nil, true, nil)
	var got struct {
		Preview clear_stale.Result `json:"preview"`
	}
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Preview.Rejected != 1 {
		t.Fatalf("json = %q %s", w.Header().Get("Content-Type"), w.Body.String())
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM stage_order WHERE symbol = 'AGQ'`).Scan(&status); err != nil || status != "REJECTED" {
		t.Errorf("a GET preview changed the row: %q %v", status, err)
	}
}

func TestClearStalePostGuardsAndLiveRun(t *testing.T) {
	h, db := clearStaleHandler(t)
	cases := []struct {
		name string
		form func() url.Values
		hdr  map[string]string
		auth bool
		want int
	}{
		{"no credentials", func() url.Values { return url.Values{"mode": {"live"}, "nonce": {"x"}} }, nil, false, http.StatusUnauthorized},
		{"forged token", func() url.Values { return url.Values{"mode": {"live"}, "nonce": {"deadbeef"}} }, nil, true, http.StatusForbidden},
		{"cross-site", func() url.Values { return url.Values{"mode": {"live"}, "nonce": {csPageNonce(t, h)}} }, map[string]string{"Sec-Fetch-Site": "cross-site"}, true, http.StatusForbidden},
		{"no mode: never guess live", func() url.Values { return url.Values{"nonce": {csPageNonce(t, h)}} }, nil, true, http.StatusBadRequest},
	}
	for _, c := range cases {
		if w := e2eDo(h, "POST", "/clear_stale", c.form(), c.auth, c.hdr); w.Code != c.want {
			t.Errorf("%s: %d, want %d", c.name, w.Code, c.want)
		}
	}
	same := map[string]string{"Sec-Fetch-Site": "same-origin"}
	if w := e2eDo(h, "POST", "/clear_stale", url.Values{"mode": {"dry"}, "nonce": {csPageNonce(t, h)}}, true, same); w.Code != http.StatusOK {
		t.Fatalf("dry post = %d", w.Code)
	}
	var status string
	if err := db.QueryRow(`SELECT status FROM stage_order WHERE symbol = 'AGQ'`).Scan(&status); err != nil || status != "REJECTED" {
		t.Fatalf("dry post changed the row: %q %v", status, err)
	}
	form := url.Values{"mode": {"live"}, "nonce": {csPageNonce(t, h)}}
	if w := e2eDo(h, "POST", "/clear_stale", form, true, same); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "rejected_orders=1") {
		t.Fatalf("live post = %d\n%s", w.Code, w.Body.String())
	}
	if err := db.QueryRow(`SELECT status FROM stage_order WHERE symbol = 'AGQ'`).Scan(&status); err != nil || status != clear_stale.StatusCleared {
		t.Errorf("live post left %q %v", status, err)
	}
	if w := e2eDo(h, "POST", "/clear_stale", form, true, same); w.Code != http.StatusForbidden {
		t.Errorf("reused token = %d, want 403", w.Code)
	}
}
