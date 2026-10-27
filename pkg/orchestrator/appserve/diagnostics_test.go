package appserve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// /runtime reveals the project and service: no anonymous access.
func TestDiagnosticsRequireDashboardAuth(t *testing.T) {
	t.Setenv("DASHBOARD_USERNAME", "op")
	t.Setenv("DASHBOARD_PASSWORD", "s3cret")
	mux := http.NewServeMux()
	if err := registerDiagnostics(mux); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/runtime"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous = %d, want 401", path, w.Code)
		}
		if w.Body.Len() > 20 && w.Body.String() != "Unauthorized\n" {
			t.Errorf("%s leaked a body to an anonymous caller: %q", path, w.Body.String())
		}

		bad := httptest.NewRequest("GET", path, nil)
		bad.SetBasicAuth("op", "wrong")
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, bad)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s wrong password = %d, want 401", path, w.Code)
		}

		ok := httptest.NewRequest("GET", path, nil)
		ok.SetBasicAuth("op", "s3cret")
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, ok)
		if w.Code == http.StatusUnauthorized || w.Code == http.StatusNotFound {
			t.Errorf("%s with the right credentials = %d", path, w.Code)
		}
	}
}

func TestDiagnosticsRefuseToStartWithoutCredentials(t *testing.T) {
	t.Setenv("DASHBOARD_USERNAME", "")
	t.Setenv("DASHBOARD_PASSWORD", "")
	if err := registerDiagnostics(http.NewServeMux()); err == nil {
		t.Fatal("must not mount open diagnostics routes when credentials are unset")
	}
}
