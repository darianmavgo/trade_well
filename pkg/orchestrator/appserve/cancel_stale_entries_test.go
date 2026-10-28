package appserve

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/cancel_stale_entries"
)

// The page's data comes from the broker, which a test cannot reach, so this renders the real
// page with a literal result: HTML by default, JSON on ?format=json.
func TestCancelStaleEntriesPageRendering(t *testing.T) {
	page := cancelStaleEntriesPage{Nonce: "abc", Preview: &cancel_stale_entries.Result{
		Accounts: 1, Cancelled: 1,
		Rows: []cancel_stale_entries.Row{{Account: "111", Symbol: "AGQ", OrderID: "555", Status: "WOULD_CANCEL", Detail: "LIMIT GOOD_TILL_CANCEL"}},
	}}
	render := func(target string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		writePage(w, httptest.NewRequest(http.MethodGet, target, nil), cancelStaleEntriesTmpl, cancel_stale_entries.JobName, "Cancel entry orders that never filled", page)
		return w
	}
	w := render("/cancel_stale_entries")
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || !strings.Contains(w.Body.String(), "AGQ") || !strings.Contains(w.Body.String(), `value="abc"`) {
		t.Errorf("default = %q %s", w.Header().Get("Content-Type"), w.Body.String())
	}
	w = render("/cancel_stale_entries?format=json")
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || !strings.Contains(w.Body.String(), `"order_id": "555"`) && !strings.Contains(w.Body.String(), `"order_id":"555"`) {
		t.Errorf("json = %q %s", w.Header().Get("Content-Type"), w.Body.String())
	}
}

// It is a cron job too: the name is in cronJobs, so /cron/cancel_stale_entries is mounted.
func TestCancelStaleEntriesIsACronJob(t *testing.T) {
	for _, n := range CronJobNames() {
		if n == cancel_stale_entries.JobName {
			return
		}
	}
	t.Errorf("%s is not in cronJobs", cancel_stale_entries.JobName)
}
