package appserve

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/exit_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
)

// registerExitMarket mounts /exit_market behind the dashboard's Basic Auth. It is NOT a
// /cron route (it is not in cronJobs or cron.yaml): it only runs when an operator posts
// the page's form. GET shows the page; a POST with the one-time token runs it, either as
// a dry run (mode=dry) or live (mode=live).
func registerExitMarket(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(handleExitMarket))
	if err != nil {
		return fmt.Errorf("/%s: %w", exit_market.JobName, err)
	}
	root.Handle("/"+exit_market.JobName, h)
	return nil
}

func handleExitMarket(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeExitMarketPage(w)
	case http.MethodPost:
		runExitMarketFromRequest(w, r)
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

func runExitMarketFromRequest(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "missing, expired or used token: reload /"+exit_market.JobName+" and submit the form again", http.StatusForbidden)
		return
	}

	// Not tied to the request: a closed tab must not abort a liquidation halfway, and
	// the killswitch (which cancels cron jobs) must not stop the tool that sells.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 9*time.Minute)
	defer cancel()
	_, db, tc, live, err := jobDepsResolved(ctx)
	if err != nil {
		http.Error(w, "cannot reach the broker or resolve the managed accounts: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no")
	fw := &flushWriter{w: w, rc: http.NewResponseController(w)}
	fmt.Fprint(fw.w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>exit_market</title>`+
		`<meta name="viewport" content="width=device-width, initial-scale=1"><style>`+pageCSS+`</style></head><body><main>`+
		`<h1>exit_market</h1><p class="sub">Running. Closing this tab does not stop it.</p><pre>`)
	fw.flush()

	opts := exit_market.Options{DryRun: mode == "dry", Killswitch: r.PostFormValue("killswitch") == "1"}
	user, _, _ := r.BasicAuth()
	log.Printf("[%s] started by %s mode=%s killswitch=%v accounts=%d", exit_market.JobName, user, mode, opts.Killswitch, len(live))
	res, err := exit_market.Run(ctx, fw, db, tc, live, opts)
	switch {
	case errors.Is(err, exit_market.ErrBusy):
		fmt.Fprintln(fw, "\nA run is already in progress; nothing was started.")
	case err != nil:
		fmt.Fprintf(fw, "\nexit_market could not run: %v\n", err)
	default:
		log.Printf("[%s] finished mode=%s sold=%d cancelled=%d failed=%d", exit_market.JobName, mode, res.Sold, res.Cancelled, res.Failed)
	}
	fmt.Fprint(fw.w, `</pre><p><a href="/`+exit_market.JobName+`">Back</a> &middot; <a href="/check_health">check_health</a> &middot; <a href="/killswitch">killswitch</a></p></main></body></html>`)
	fw.flush()
}

func writeExitMarketPage(w http.ResponseWriter) {
	ks := ""
	if cfg, err := appconfig.Load(); err == nil {
		if db, err := appconfig.OpenDB(cfg.DBPath); err == nil {
			if st, err := killswitch.Current(db); err == nil && st.Engaged {
				ks = `<div class="card">` + html.EscapeString(st.String()) + `. exit_market still works while it is engaged.</div>`
			}
			_ = db.Close()
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>exit_market</title><style>%s</style></head><body><main>
<h1>exit_market</h1>
<p class="sub">For every managed account (<code>SCHWAB_ACCOUNT_NUMBERS</code>): cancel every working order, then sell every long stock and ETF position at market. Shorts, options, mutual funds and fractional shares are reported and skipped. It is not a cron job and never runs on a schedule.</p>
%s<div class="card"><strong class="bad">Selling is LIVE.</strong> The second button below sells every position in every managed account at market and cancels all their working orders. Use the preview first. Nothing runs when this page loads.</div>
<form method="post" action="/%s">
<input type="hidden" name="nonce" value="%s">
<p><label><input type="checkbox" name="killswitch" value="1" checked> Then engage the killswitch, so the next cron does not stage and buy it all back</label></p>
<p><button type="submit" name="mode" value="dry" class="safe">Preview: show what would be cancelled and sold (changes nothing)</button></p>
<p><button type="submit" name="mode" value="live">SELL EVERYTHING AT MARKET</button></p>
</form>
<p class="sub">Also: <a href="/killswitch">/killswitch</a> turns cron off without selling. Each run needs a fresh page load (the token is single use).</p>
</main></body></html>`, pageCSS+`.safe{color:var(--fg);border-color:var(--line)}`, ks, exit_market.JobName, pageNonces.issue())
}
