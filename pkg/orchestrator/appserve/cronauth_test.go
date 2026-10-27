package appserve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func bearer(token string) *http.Request {
	r := httptest.NewRequest("GET", "/cron/scan_market", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func TestAuthorizeCron(t *testing.T) {
	t.Run("app engine cron header", func(t *testing.T) {
		r := httptest.NewRequest("GET", "/cron/scan_market", nil)
		r.Header.Set("X-Appengine-Cron", "true")
		if _, ok, _ := authorizeCron(r); !ok {
			t.Fatal("cron header must pass")
		}
	})
	t.Run("manual off by default", func(t *testing.T) {
		t.Setenv(EnvCronAllowedEmails, "")
		if _, ok, _ := authorizeCron(bearer("any-token")); ok {
			t.Fatal("a bearer token must not pass when CRON_ALLOWED_EMAILS is unset")
		}
	})
	t.Run("forged token refused", func(t *testing.T) {
		t.Setenv(EnvCronAllowedEmails, "me@example.com")
		if _, ok, _ := authorizeCron(bearer("forged")); ok {
			t.Fatal("a token that fails Google validation must be refused")
		}
	})
	t.Run("no credentials", func(t *testing.T) {
		t.Setenv(EnvCronAllowedEmails, "me@example.com")
		if _, ok, _ := authorizeCron(bearer("")); ok {
			t.Fatal("no credentials must be refused")
		}
	})
}

// A real Google-signed token cannot be made offline, so the signature, expiry and audience check
// (idtoken.Validate) is untested here; emailAllowed is the part that decides from the claims.
func TestEmailAllowed(t *testing.T) {
	allowed := []string{"me@example.com"}
	cases := []struct {
		name   string
		claims map[string]any
		ok     bool
		email  string
	}{
		{"listed, case-insensitive", map[string]any{"email": "Me@Example.com", "email_verified": true}, true, "me@example.com"},
		{"other email", map[string]any{"email": "someone.else@example.com", "email_verified": true}, false, ""},
		{"unverified email", map[string]any{"email": "me@example.com", "email_verified": false}, false, ""},
		{"no email", map[string]any{"email_verified": true}, false, ""},
	}
	for _, c := range cases {
		email, ok, why := emailAllowed(c.claims, allowed)
		if ok != c.ok || email != c.email {
			t.Errorf("%s: ok=%v email=%q why=%q", c.name, ok, email, why)
		}
	}
}

func TestCronJobNamesMatchRoutes(t *testing.T) {
	mux := http.NewServeMux()
	registerCron(mux)
	for _, name := range CronJobNames() {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/cron/"+name, nil))
		if w.Code != http.StatusForbidden {
			t.Errorf("/cron/%s = %d, want 403 (route exists, no credentials)", name, w.Code)
		}
	}
}
