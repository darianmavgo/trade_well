package appserve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/configview"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategy_allowlist"
)

// The page reads the stored list from the GCS config object, which a test cannot reach, so
// these tests cover what does not need it: the two renderings of a real view with the real
// template, and every refusal a POST makes before storage is touched. Reading and writing the
// object (strategy_allowlist.Update) is untested here; it is the same code as the CLI.
func sampleAllowlistView() allowlistView {
	return allowlistView{
		Current: []string{"hmm-calm-soxl"},
		Text:    "hmm-calm-soxl",
		Choices: []configview.Choice{
			{ID: "hmm-calm-soxl", Name: "SOXL Long While QQQ Calm (HMM)", Selected: true, OK: true},
			{ID: "hmm-calm-qqq", Name: "QQQ", OK: false, Problem: "hmm-calm-qqq is a row missing from the bundle"},
		},
		Change: &strategy_allowlist.Change{Before: []string{"a"}, After: []string{"hmm-calm-soxl"}},
		Added:  []string{"hmm-calm-soxl"}, Removed: []string{"a"},
		Saved: true, Nonce: "abc123",
	}
}

func TestAllowlistPageIsHTMLByDefaultAndJSONOnRequest(t *testing.T) {
	v := sampleAllowlistView()
	for _, tc := range []struct{ url, ctype string }{{"/allowlist", "text/html"}, {"/allowlist?format=json", "application/json"}} {
		rec := httptest.NewRecorder()
		writePage(rec, httptest.NewRequest(http.MethodGet, tc.url, nil), allowlistPage, "Strategy allowlist", "sub", v)
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), tc.ctype) {
			t.Fatalf("%s content type %q", tc.url, rec.Header().Get("Content-Type"))
		}
		body := rec.Body.String()
		if tc.ctype == "application/json" {
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if _, leaked := got["Nonce"]; leaked || strings.Contains(body, "abc123") {
				t.Fatal("the one-time token must not be in the JSON")
			}
			continue
		}
		for _, want := range []string{`name="list"`, `name="pick" value="hmm-calm-soxl" checked`, `name="pick" value="hmm-calm-qqq" disabled`, `value="abc123"`, "redeploy", "Preview"} {
			if !strings.Contains(body, want) {
				t.Errorf("HTML lacks %q", want)
			}
		}
	}
}

func TestAllowlistPostRefusals(t *testing.T) {
	h := pagesRoot(t)
	mux := http.NewServeMux()
	if err := registerAllowlistPage(mux); err != nil {
		t.Fatal(err)
	}
	post := func(form url.Values, hdr map[string]string, auth bool) int {
		req := httptest.NewRequest(http.MethodPost, "/allowlist", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if auth {
			req.SetBasicAuth("u", "p")
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	_ = h // pagesRoot sets the dashboard credentials these requests use
	ok := url.Values{"do": {"save"}, "mode": {"text"}, "list": {"a"}, "nonce": {"never-issued"}}
	if c := post(ok, nil, false); c != http.StatusUnauthorized {
		t.Errorf("no credentials = %d, want 401", c)
	}
	if c := post(ok, map[string]string{"Sec-Fetch-Site": "cross-site"}, true); c != http.StatusForbidden {
		t.Errorf("cross-site = %d, want 403", c)
	}
	if c := post(ok, nil, true); c != http.StatusForbidden {
		t.Errorf("unissued token = %d, want 403", c)
	}
	bad := url.Values{"do": {"delete"}, "mode": {"text"}, "nonce": {"x"}}
	if c := post(bad, nil, true); c != http.StatusBadRequest {
		t.Errorf("bad do = %d, want 400", c)
	}
	bad = url.Values{"do": {"save"}, "mode": {"other"}, "nonce": {"x"}}
	if c := post(bad, nil, true); c != http.StatusBadRequest {
		t.Errorf("bad mode = %d, want 400", c)
	}
}
