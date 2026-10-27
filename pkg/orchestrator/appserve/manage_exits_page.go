package appserve

import (
	"fmt"
	"net/http"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
)

// registerManageExitsPage mounts /manage_exits behind the dashboard's Basic Auth so an
// operator can run the manage_exits job from a browser. /cron/manage_exits itself only
// admits App Engine cron, so a browser gets "forbidden" there. This page calls the same
// handler (handleCronManageExits): GET shows the page, POST with the one-time token runs
// it, as a preview (?dry_run=1: adopts, sells and writes nothing) or live.
func registerManageExitsPage(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(handleManageExitsPage))
	if err != nil {
		return fmt.Errorf("/manage_exits: %w", err)
	}
	root.Handle("/manage_exits", h)
	return nil
}

func handleManageExitsPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>manage_exits</title><style>%s</style></head><body><main>
<h1>manage_exits</h1>
<p class="sub">Adopts held positions that have no manage_exit row (exit date from the entry fill), then closes positions whose exit date has arrived or whose target is hit. Nothing runs when this page loads.</p>
<div class="card"><strong class="bad">Live is LIVE.</strong> It sells due positions at market. Use the preview first: it changes nothing and lists each row.</div>
<form method="post" action="/manage_exits">
<input type="hidden" name="nonce" value="%s">
<p><button type="submit" name="mode" value="dry">Preview (changes nothing)</button></p>
<p><button type="submit" name="mode" value="live">RUN LIVE</button></p>
</form></main></body></html>`, pageCSS, pageNonces.issue())
	case http.MethodPost:
		if !sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		mode := r.PostFormValue("mode")
		if mode != "dry" && mode != "live" {
			http.Error(w, "mode must be dry or live", http.StatusBadRequest)
			return
		}
		if !pageNonces.consume(r.PostFormValue("nonce")) {
			http.Error(w, "missing, expired or used token: reload /manage_exits and submit again", http.StatusForbidden)
			return
		}
		q := r.URL.Query()
		if mode == "dry" {
			q.Set("dry_run", "1")
		}
		r.URL.RawQuery = q.Encode()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		handleCronManageExits(w, r)
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

// registerCheckHealthRun mounts POST /check_health/run behind the dashboard's Basic Auth:
// the "Run check_health now" button on /check_health. It runs the same handler as
// /cron/check_health (which only admits App Engine cron), then sends the browser back to
// the page. check_health never places or cancels an order, so no single-use token is
// needed; the same-origin check keeps other sites from triggering it.
func registerCheckHealthRun(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only (use the button on /check_health)", http.StatusMethodNotAllowed)
			return
		}
		if !sameOrigin(r) {
			http.Error(w, "cross-site request refused", http.StatusForbidden)
			return
		}
		cw := &captureWriter{header: http.Header{}, code: http.StatusOK}
		handleCronCheckHealth(cw, r)
		if cw.code >= 400 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(cw.code)
			_, _ = w.Write(cw.body)
			return
		}
		http.Redirect(w, r, "/check_health", http.StatusSeeOther)
	}))
	if err != nil {
		return fmt.Errorf("/check_health/run: %w", err)
	}
	root.Handle("/check_health/run", h)
	return nil
}

// captureWriter holds a handler's status and body so the caller can decide what to send:
// a redirect on success, the handler's own error text on failure.
type captureWriter struct {
	header http.Header
	code   int
	body   []byte
}

func (c *captureWriter) Header() http.Header  { return c.header }
func (c *captureWriter) WriteHeader(code int) { c.code = code }
func (c *captureWriter) Write(b []byte) (int, error) {
	c.body = append(c.body, b...)
	return len(b), nil
}
