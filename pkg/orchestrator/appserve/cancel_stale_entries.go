package appserve

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/cancel_stale_entries"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
)

// handleCronCancelStaleEntries cancels the entry orders that have rested unfilled for over 24
// hours, in every managed account. Add ?dry_run=1 to report without cancelling anything.
func handleCronCancelStaleEntries(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Minute)
	defer cancel()
	_, db, tc, live, err := jobDepsResolved(ctx)
	if err != nil {
		writeJobErr(w, cancel_stale_entries.JobName, err)
		return
	}
	defer db.Close()
	dryRun := r.URL.Query().Get("dry_run") == "1"
	var report bytes.Buffer
	res, err := cancel_stale_entries.Run(ctx, &report, db, tc, live, cancel_stale_entries.Options{DryRun: dryRun})
	if err != nil {
		writeJobErr(w, cancel_stale_entries.JobName, err)
		return
	}
	log.Printf("[%s] accounts=%d cancelled=%d failed=%d dry_run=%v", cancel_stale_entries.JobName, res.Accounts, res.Cancelled, res.Failed, dryRun)
	summary := fmt.Sprintf("%s accounts=%d cancelled=%d failed=%d", cancel_stale_entries.JobName, res.Accounts, res.Cancelled, res.Failed)
	if res.Failed > 0 {
		http.Error(w, "FAILED "+summary, http.StatusInternalServerError)
		return
	}
	fmt.Fprintf(w, "OK %s dry_run=%v\n%s", summary, dryRun, report.String())
}

// registerCancelStaleEntries mounts /cancel_stale_entries behind the dashboard's Basic Auth.
// GET previews what would be cancelled (a dry run against the broker; HTML, or JSON with
// ?format=json). A POST with the one-time token runs it, as a dry run (mode=dry) or live
// (mode=live).
func registerCancelStaleEntries(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(handleCancelStaleEntries))
	if err != nil {
		return fmt.Errorf("/%s: %w", cancel_stale_entries.JobName, err)
	}
	root.Handle("/"+cancel_stale_entries.JobName, h)
	return nil
}

func handleCancelStaleEntries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		previewCancelStaleEntries(w, r)
	case http.MethodPost:
		runCancelStaleEntriesFromRequest(w, r)
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

type cancelStaleEntriesPage struct {
	Nonce   string                       `json:"-"`
	Preview *cancel_stale_entries.Result `json:"preview"`
}

var cancelStaleEntriesTmpl = newPage("cancel_stale_entries", `
<div class="card">Unfilled entry orders that have been resting at the broker for more than 24 hours (good-till-cancelled entries that never filled) hold buying power. They are cancelled, and their staged order becomes CANCELLED. Only entry orders (a BUY, still working) are cancelled: exits, stops and targets of a filled position are never touched. Add <code>?format=json</code> for the data.</div>
<h2>Would cancel: {{.D.Preview.Cancelled}} order(s)</h2>
<table><thead><tr><th>Account</th><th>Symbol</th><th>Order</th><th>Status</th><th>Detail</th></tr></thead><tbody>
{{range .D.Preview.Rows}}<tr><td>{{.Account}}</td><td>{{.Symbol}}</td><td>{{.OrderID}}</td><td>{{.Status}}</td><td class="sum">{{.Detail}}</td></tr>
{{else}}<tr><td colspan="5">Nothing to report.</td></tr>{{end}}
</tbody></table>
<form method="post" action="/cancel_stale_entries">
<input type="hidden" name="nonce" value="{{.D.Nonce}}">
<p><button type="submit" name="mode" value="dry">Preview again (changes nothing)</button>
<button type="submit" name="mode" value="live" class="live">CANCEL THESE ORDERS</button></p>
</form>
<p class="muted">Each run needs a fresh page load (the token is single use). The same job runs daily from cron.</p>`)

func previewCancelStaleEntries(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	_, db, tc, live, err := jobDepsResolved(ctx)
	if err != nil {
		http.Error(w, "cannot reach the broker or resolve the managed accounts: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()
	res, err := cancel_stale_entries.Run(ctx, io.Discard, db, tc, live, cancel_stale_entries.Options{DryRun: true})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	page := cancelStaleEntriesPage{Nonce: pageNonces.issue(), Preview: res}
	writePage(w, r, cancelStaleEntriesTmpl, cancel_stale_entries.JobName, "Cancel entry orders that never filled", page)
}

func runCancelStaleEntriesFromRequest(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "missing, expired or used token: reload /"+cancel_stale_entries.JobName+" and submit the form again", http.StatusForbidden)
		return
	}
	// Not tied to the request: a closed tab must not abort a cancel halfway.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 8*time.Minute)
	defer cancel()
	_, db, tc, live, err := jobDepsResolved(ctx)
	if err != nil {
		http.Error(w, "cannot reach the broker or resolve the managed accounts: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()
	user, _, _ := r.BasicAuth()
	log.Printf("[%s] started by %s mode=%s accounts=%d", cancel_stale_entries.JobName, user, mode, len(live))
	var report bytes.Buffer
	_, err = cancel_stale_entries.Run(ctx, &report, db, tc, live, cancel_stale_entries.Options{DryRun: mode == "dry"})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>cancel_stale_entries</title>`+
		`<meta name="viewport" content="width=device-width, initial-scale=1"><style>`+pageCSS+`</style></head><body><main><h1>cancel_stale_entries</h1><pre>`)
	fmt.Fprint(w, html.EscapeString(report.String()))
	switch {
	case errors.Is(err, cancel_stale_entries.ErrBusy):
		fmt.Fprintln(w, "\nA run is already in progress; nothing was started.")
	case err != nil:
		fmt.Fprintf(w, "\ncancel_stale_entries could not run: %s\n", html.EscapeString(err.Error()))
	}
	fmt.Fprint(w, `</pre><p><a href="/`+cancel_stale_entries.JobName+`">Back</a> &middot; <a href="/orders">orders</a></p></main></body></html>`)
}
