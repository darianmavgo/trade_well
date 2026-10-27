package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func getStatus(h http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", path, nil)
	req.SetBasicAuth("admin", "s3cr3t")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// A browser visit gets a styled page, never raw JSON; JSON is opt-in.
func TestStatusPageIsHTMLByDefaultAndJSONOnRequest(t *testing.T) {
	_, h := checkHealthServer(t)

	w := getStatus(h, "/status/pdd_tree")
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("default = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, want := range []string{"<title>status pdd_tree</title>", "Problems", "Today&#39;s signal check", "Positions", "Staged orders", "?format=json"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(body), "{") {
		t.Error("default response is raw JSON")
	}

	j := getStatus(h, "/status/pdd_tree?format=json")
	if !strings.HasPrefix(j.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("format=json content type %q", j.Header().Get("Content-Type"))
	}
	var got map[string]any
	if err := json.Unmarshal(j.Body.Bytes(), &got); err != nil || got["strategy"] != "pdd_tree" {
		t.Errorf("json = %v err %v", got, err)
	}
}

func TestStatusPageRejectsBadRequests(t *testing.T) {
	_, h := checkHealthServer(t)
	for path, want := range map[string]int{"/status": 400, "/status/": 400, "/status/a/b": 400, "/status/x?days=0": 400, "/status/x?days=abc": 400} {
		if w := getStatus(h, path); w.Code != want {
			t.Errorf("%s = %d, want %d", path, w.Code, want)
		}
	}
	req := httptest.NewRequest("GET", "/status/x", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("no credentials = %d, want 401", w.Code)
	}
}

func TestStrategyPageListsEveryGreenlitStrategyAsHTML(t *testing.T) {
	t.Setenv("STRATEGY_ALLOWLIST", "sig-voo-buy-tecl+pdd_tree,tsll-daily-one-share")
	_, h := checkHealthServer(t)

	w := getStatus(h, "/strategy")
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("default = %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	for _, id := range []string{"sig-voo-buy-tecl", "pdd_tree", "tsll-daily-one-share"} {
		if !strings.Contains(body, `href="/status/`+id+`"`) {
			t.Errorf("no row linking to /status/%s", id)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(body), "[") {
		t.Error("default response is raw JSON")
	}

	j := getStatus(h, "/strategy?format=json")
	var got []map[string]any
	if err := json.Unmarshal(j.Body.Bytes(), &got); err != nil || len(got) != 3 {
		t.Errorf("json = %d strategies, err %v", len(got), err)
	}
	if w := getStatus(h, "/strategy/pdd_tree"); w.Code != http.StatusNotFound {
		t.Errorf("/strategy/pdd_tree = %d, want 404 (the per-strategy page is /status/{id})", w.Code)
	}
}

func TestStrategyPageWithNoAllowlist(t *testing.T) {
	t.Setenv("STRATEGY_ALLOWLIST", "")
	_, h := checkHealthServer(t)
	if body := getStatus(h, "/strategy").Body.String(); !strings.Contains(body, "no strategy is greenlit") {
		t.Error("empty allowlist must say so")
	}
}
