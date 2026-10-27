package appserve

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/clear_stale"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

// registerClearStale mounts /clear_stale behind the dashboard's Basic Auth. It is NOT a /cron
// route: it only changes data when an operator posts the page's form. GET previews what would
// be cleared (HTML, or JSON with ?format=json); a POST with the one-time token runs it, as a
// dry run (mode=dry) or live (mode=live). It never contacts the broker.
func registerClearStale(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(handleClearStale))
	if err != nil {
		return fmt.Errorf("/%s: %w", clear_stale.JobName, err)
	}
	root.Handle("/"+clear_stale.JobName, h)
	return nil
}

func handleClearStale(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		previewClearStale(w, r)
	case http.MethodPost:
		runClearStaleFromRequest(w, r)
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

type clearStalePage struct {
	Nonce    string              `json:"-"`
	Greenlit []string            `json:"greenlit"`
	Preview  *clear_stale.Result `json:"preview"`
}

var clearStaleTmpl = newPage("clear_stale", `
<div class="card">Strategies that are not greenlit ({{range $i, $g := .D.Greenlit}}{{if $i}}, {{end}}{{$g}}{{end}}) can no longer stage, submit or manage anything, so their REJECTED or PENDING staged orders and their OPEN trades the broker no longer holds are retired in Turso, and check_health stops reporting them. Greenlit strategies and positions the broker still holds are never touched. Nothing is deleted and the broker is not contacted. Add <code>?format=json</code> for the data.</div>
<h2>Would clear: {{.D.Preview.Rejected}} rejected, {{.D.Preview.Pending}} pending, {{.D.Preview.Orphans}} orphan trades ({{.D.Preview.Kept}} kept)</h2>
<table><thead><tr><th>Kind</th><th>Account</th><th>Symbol</th><th>Id</th><th>Detail</th></tr></thead><tbody>
{{range .D.Preview.Rows}}<tr><td>{{.Kind}}</td><td>{{.Account}}</td><td>{{.Symbol}}</td><td>{{.ID}}</td><td class="sum">{{.Detail}}</td></tr>
{{else}}<tr><td colspan="5">Nothing is stale.</td></tr>{{end}}
</tbody></table>
<form method="post" action="/clear_stale">
<input type="hidden" name="nonce" value="{{.D.Nonce}}">
<p><button type="submit" name="mode" value="dry">Preview again (changes nothing)</button>
<button type="submit" name="mode" value="live" class="live">CLEAR THESE ROWS</button></p>
</form>
<p class="muted">Each run needs a fresh page load (the token is single use). Run account_state first if kept trades say the mirror is older than the fill.</p>`)

// clearStaleOptions reads the greenlit list from STRATEGY_ALLOWLIST, the same source the cron
// jobs use. It errors when the list is empty rather than treating every strategy as retired.
func clearStaleOptions(dry bool) (clear_stale.Options, error) {
	allow, err := strategies.Parse(os.Getenv(strategies.EnvAllowlist))
	if err != nil {
		return clear_stale.Options{}, err
	}
	return clear_stale.Options{DryRun: dry, Allowlist: allow}, nil
}

func previewClearStale(w http.ResponseWriter, r *http.Request) {
	cfg, err := appconfig.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	db, err := openAndEnsureSchemas(cfg.DBPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()
	opts, err := clearStaleOptions(true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var report bytes.Buffer
	res, err := clear_stale.Run(r.Context(), &report, db, opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	page := clearStalePage{Nonce: pageNonces.issue(), Greenlit: opts.Allowlist, Preview: res}
	writePage(w, r, clearStaleTmpl, clear_stale.JobName, "Stop reporting on strategies you no longer run", page)
}

func runClearStaleFromRequest(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "missing, expired or used token: reload /"+clear_stale.JobName+" and submit the form again", http.StatusForbidden)
		return
	}
	cfg, err := appconfig.Load()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	db, err := openAndEnsureSchemas(cfg.DBPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
	defer cancel()
	opts, err := clearStaleOptions(mode == "dry")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	user, _, _ := r.BasicAuth()
	log.Printf("[%s] started by %s mode=%s", clear_stale.JobName, user, mode)
	var report bytes.Buffer
	_, err = clear_stale.Run(ctx, &report, db, opts)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>clear_stale</title>`+
		`<meta name="viewport" content="width=device-width, initial-scale=1"><style>`+pageCSS+`</style></head><body><main><h1>clear_stale</h1><pre>`)
	fmt.Fprint(w, html.EscapeString(report.String()))
	switch {
	case errors.Is(err, clear_stale.ErrBusy):
		fmt.Fprintln(w, "\nA run is already in progress; nothing was started.")
	case err != nil:
		fmt.Fprintf(w, "\nclear_stale could not run: %s\n", html.EscapeString(err.Error()))
	}
	fmt.Fprint(w, `</pre><p><a href="/`+clear_stale.JobName+`">Back</a> &middot; <a href="/check_health">check_health</a></p></main></body></html>`)
}
