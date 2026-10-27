package dashboard

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
)

func TestRiskPageIsHTMLByDefaultAndJSONOnRequest(t *testing.T) {
	srv, h := checkHealthServer(t)
	if err := risk.EnsureSchema(srv.db); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.db.Exec(`INSERT INTO risk_state (account_number, assessed_at, session_date, equity, peak, drawdown, day_change, gross, scale, halt_entries, max_gross, reasons) VALUES ('111',?,?,?,?,?,?,?,?,0,1.0,'drawdown 12.0% is past the 10% step (entries at 50%)')`,
		time.Now().UTC(), "2026-10-15", 880.0, 1000.0, 0.12, -0.01, 0.4, 0.5); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.db.Exec(`INSERT INTO risk_equity VALUES ('111','2026-10-15',880,'now')`); err != nil {
		t.Fatal(err)
	}

	w := getStatus(h, "/risk")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("default = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, want := range []string{"<title>risk</title>", "entries at 50% size", "12.0%", "?format=json", "$880.00"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("page missing %q", want)
		}
	}

	j := getStatus(h, "/risk?format=json")
	if !strings.HasPrefix(j.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("format=json content type %q", j.Header().Get("Content-Type"))
	}
	var v struct {
		Accounts []struct {
			Account  string  `json:"account"`
			ScalePct float64 `json:"entry_size_pct"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(j.Body.Bytes(), &v); err != nil || len(v.Accounts) != 1 || v.Accounts[0].ScalePct != 50 {
		t.Fatalf("json = %s (%v)", j.Body.String(), err)
	}
}
