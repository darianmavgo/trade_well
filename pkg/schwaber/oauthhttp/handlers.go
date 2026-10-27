package oauthhttp

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/schwab"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

const (
	oauthStateCookie = "schwab_oauth_state"
	oauthStateTTL    = 15 * time.Minute
)

// registerSchwabAuth mounts OAuth re-auth endpoints.
//
//	GET /auth/schwab          — basic-auth gated; redirects to Schwab authorize URL
//	GET /auth/schwab/callback — Schwab redirect target (no basic auth); exchanges
//	                            code, writes token.json under /tmp, publishes a new
//	                            Secret Manager version, then redirects to /schwabstate
func Register(mux *http.ServeMux) {
	mux.HandleFunc("/auth/schwab", requireDashboardBasicAuth(handleSchwabAuthStart))
	mux.HandleFunc("/auth/schwab/callback", handleSchwabAuthCallback)
}

func requireDashboardBasicAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := os.Getenv("DASHBOARD_USERNAME")
		pass := os.Getenv("DASHBOARD_PASSWORD")
		if user == "" || pass == "" {
			http.Error(w, "DASHBOARD_USERNAME/PASSWORD not configured", http.StatusInternalServerError)
			return
		}
		u, p, ok := r.BasicAuth()
		if !ok ||
			subtle.ConstantTimeCompare([]byte(u), []byte(user)) != 1 ||
			subtle.ConstantTimeCompare([]byte(p), []byte(pass)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Trading Dashboard"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func handleSchwabAuthStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := config.Load()
	callback := schwabCallbackURL(r)
	cfg.CallbackURL = callback
	ensureTokenPath(cfg)

	state, err := randomHex(16)
	if err != nil {
		http.Error(w, "failed to create oauth state", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/auth/schwab",
		MaxAge:   int(oauthStateTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})

	tc, err := trader.NewTraderClient(r.Context(), cfg)
	if err != nil {
		http.Error(w, "schwab client: "+err.Error(), http.StatusInternalServerError)
		return
	}

	authURL := tc.Client.AuthorizeURL()
	// Append CSRF state (Schwab echoes unknown params on some flows; we also
	// keep the cookie as source of truth and accept ?state= when present).
	if strings.Contains(authURL, "?") {
		authURL += "&state=" + state
	} else {
		authURL += "?state=" + state
	}

	logSchwabAuthStart(callback, cfg.AppKey, authURL)
	http.Redirect(w, r, authURL, http.StatusFound)
}

func handleSchwabAuthCallback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cookie, err := r.Cookie(oauthStateCookie)
	if err != nil || cookie.Value == "" {
		http.Error(w, "missing oauth state cookie — start at /auth/schwab", http.StatusBadRequest)
		return
	}
	if qs := r.URL.Query().Get("state"); qs != "" &&
		subtle.ConstantTimeCompare([]byte(qs), []byte(cookie.Value)) != 1 {
		http.Error(w, "oauth state mismatch", http.StatusBadRequest)
		return
	}
	// Clear state cookie
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    "",
		Path:     "/auth/schwab",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
	})

	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		http.Error(w, "schwab oauth error: "+errMsg+" — "+r.URL.Query().Get("error_description"), http.StatusBadRequest)
		return
	}

	code, err := schwab.ExtractCodeFromURL(r.URL.String())
	if err != nil || code == "" {
		code = r.URL.Query().Get("code")
	}
	if code == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}

	cfg := config.Load()
	cfg.CallbackURL = schwabCallbackURL(r)
	ensureTokenPath(cfg)

	tc, err := trader.NewTraderClient(r.Context(), cfg)
	if err != nil {
		http.Error(w, "schwab client: "+err.Error(), http.StatusInternalServerError)
		return
	}

	tok, err := tc.Client.ExchangeAuthCode(r.Context(), code)
	if err != nil {
		log.Printf("[gae] schwab token exchange: %v", err)
		http.Error(w, "token exchange failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	// tc.Client.ExchangeAuthCode already saved the token and pushed it to
	// GCS (tokensync.SyncAfterSave, called from saveToken) — nothing else
	// to publish here.
	log.Printf("[gae] schwab auth ok — token written to %s (refresh issued %s)",
		cfg.TokenPath, tok.RefreshTokenIssued.Format(time.RFC3339))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Schwab auth</title></head><body>
<h1>Schwab re-auth succeeded</h1>
<p>Token saved to <code>%s</code> and pushed to GCS.</p>
<p><a href="/schwabstate">Go to schwabstate</a></p></body></html>`, cfg.TokenPath)
}

func logSchwabAuthStart(callback, appKey, authURL string) {
	u, err := url.Parse(authURL)
	if err != nil {
		log.Printf("[gae] schwab auth start callback=%s app_key=%s authURL_parse_err=%v",
			callback, redactKey(appKey), err)
		return
	}
	q := u.Query()
	log.Printf("[gae] schwab auth start callback=%s redirect_uri=%s client_id=%s auth_host=%s path=%s state=%t",
		callback,
		q.Get("redirect_uri"),
		redactKey(q.Get("client_id")),
		u.Host,
		u.Path,
		q.Get("state") != "",
	)
}

func redactKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "<empty>"
	}
	if len(s) <= 8 {
		return s[:2] + "…"
	}
	return s[:6] + "…" + s[len(s)-4:]
}

func schwabCallbackURL(r *http.Request) string {
	if u := strings.TrimSpace(os.Getenv("SCHWAB_CALLBACK_URL")); u != "" {
		return u
	}
	host := r.Host
	if host == "" {
		host = "trade-orchestrator-508722.uc.r.appspot.com"
	}
	return "https://" + host + "/auth/schwab/callback"
}

func ensureTokenPath(cfg *config.Config) {
	folder := envOr("APP_FOLDER", "/tmp/trade_orchestrator")
	_ = os.MkdirAll(folder, 0o755)
	if cfg.TokenPath == "" || cfg.TokenPath == "token.json" || !filepath.IsAbs(cfg.TokenPath) {
		cfg.TokenPath = filepath.Join(folder, "token.json")
	}
	_ = os.Setenv("SCHWAB_TOKEN_PATH", cfg.TokenPath)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
