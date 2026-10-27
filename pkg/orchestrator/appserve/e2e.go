package appserve

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/run_e2e_live"
)

// CronHandler is the http.Handler serving every /cron/<job> route, the same one
// the server mounts. run_e2e_live calls it in-process so its steps go through
// exactly the wrappers a scheduled run does.
func CronHandler() http.Handler {
	mux := http.NewServeMux()
	registerCron(mux)
	return mux
}

// pageNonceTTL is how long the page's one-time token stays valid.
const pageNonceTTL = 10 * time.Minute

// nonces are one-time tokens handed out by GET /run_e2e_live and required by the
// POST that runs the pipeline. Basic Auth alone is not enough: Chrome attaches
// cached Basic credentials to a POST sent by any other site, so without a token
// another page could start a live run. A page from another origin cannot read
// this one, so it cannot obtain a token.
type nonces struct {
	mu sync.Mutex
	m  map[string]time.Time
}

var pageNonces = &nonces{m: map[string]time.Time{}}

func (n *nonces) issue() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	tok := hex.EncodeToString(b)
	n.mu.Lock()
	defer n.mu.Unlock()
	now := time.Now()
	for k, exp := range n.m { // drop expired tokens
		if now.After(exp) {
			delete(n.m, k)
		}
	}
	n.m[tok] = now.Add(pageNonceTTL)
	return tok
}

// valid reports whether tok is issued and unexpired, without using it up.
func (n *nonces) valid(tok string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	exp, ok := n.m[tok]
	return ok && time.Now().Before(exp)
}

// consume uses tok up; it reports whether it was valid.
func (n *nonces) consume(tok string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	exp, ok := n.m[tok]
	delete(n.m, tok)
	return ok && time.Now().Before(exp)
}

// registerE2E mounts /run_e2e_live behind the dashboard's Basic Auth (the same
// middleware and credentials as every dashboard route). GET shows the page; only
// a POST with the page's one-time token (the button) runs the pipeline, because
// running it places real orders.
func registerE2E(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(handleRunE2ELive))
	if err != nil {
		return fmt.Errorf("/%s: %w", run_e2e_live.JobName, err)
	}
	root.Handle("/"+run_e2e_live.JobName, h)
	return nil
}

func handleRunE2ELive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeE2EPage(w)
	case http.MethodPost:
		runE2EFromRequest(w, r)
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

// sameOrigin rejects a POST that a browser says came from another site.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host {
			return false
		}
	}
	return true
}

func runE2EFromRequest(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !pageNonces.valid(r.PostFormValue("nonce")) {
		http.Error(w, "missing or expired token: reload /"+run_e2e_live.JobName+" and submit the form again", http.StatusForbidden)
		return
	}
	if !pageNonces.consume(r.PostFormValue("nonce")) { // single use
		http.Error(w, "token already used: reload the page", http.StatusForbidden)
		return
	}

	cfg, err := appconfig.Load()
	if err != nil {
		http.Error(w, "config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	db, err := bootstrap.OpenDB(cfg.DBPath)
	if err != nil {
		http.Error(w, "database: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	if st, err := killswitch.Current(db); err == nil && st.Engaged {
		http.Error(w, st.String()+" -- run_e2e_live places real orders and did not run. Release the killswitch at /killswitch first.", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Accel-Buffering", "no") // let nginx pass lines through as they are written
	fw := &flushWriter{w: w, rc: http.NewResponseController(w)}
	fmt.Fprint(fw.w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>run_e2e_live</title>`+
		`<meta name="viewport" content="width=device-width, initial-scale=1"><style>`+pageCSS+`</style></head><body><main>`+
		`<h1>run_e2e_live</h1><p class="sub">Running. Closing this tab does not stop the run.</p><pre>`)
	fw.flush()

	// A closed tab must not abort a run halfway through submitting orders.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 9*time.Minute)
	defer cancel()
	// The killswitch stops this run too: its steps are refused once it is engaged.
	ctx, doneTrack := killswitch.Track(ctx, run_e2e_live.JobName)
	defer doneTrack()
	log.Printf("[%s] run started by an authenticated dashboard user oracle=%v", run_e2e_live.JobName, r.PostFormValue("oracle") == "1")
	res, err := run_e2e_live.Run(ctx, fw, db, CronHandler(), run_e2e_live.Options{Oracle: r.PostFormValue("oracle") == "1"})
	switch {
	case errors.Is(err, run_e2e_live.ErrBusy):
		fmt.Fprintln(fw, "\nA run is already in progress; nothing was started.")
	case err != nil:
		fmt.Fprintf(fw, "\nrun_e2e_live could not start: %v\n", err)
	default:
		log.Printf("[%s] run finished passed=%v failures=%d", run_e2e_live.JobName, res.Passed(), len(res.Failures))
	}
	fmt.Fprint(fw.w, `</pre><p><a href="/`+run_e2e_live.JobName+`">Back</a> &middot; <a href="/check_health">check_health</a></p></main></body></html>`)
	fw.flush()
}

// flushWriter HTML-escapes what Run narrates and flushes it, so Chrome shows each
// line as it happens. Write errors (a closed tab) are ignored: the run goes on.
type flushWriter struct {
	w  http.ResponseWriter
	rc *http.ResponseController
}

func (f *flushWriter) Write(p []byte) (int, error) {
	_, _ = fmt.Fprint(f.w, html.EscapeString(string(p)))
	f.flush()
	return len(p), nil
}

func (f *flushWriter) flush() { _ = f.rc.Flush() }

const pageCSS = `:root{--bg:#fff;--fg:#1c1f24;--muted:#5b6470;--line:#dde1e6;--bad:#c62828;--card:#f6f8fa;--code:#eef1f4}
@media (prefers-color-scheme:dark){:root{--bg:#0f1216;--fg:#e6e9ed;--muted:#98a2b0;--line:#2a313a;--bad:#ef6b6b;--card:#171b21;--code:#1f252d}}
body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.55 -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
main{max-width:1100px;margin:0 auto;padding:24px 16px 64px}h1{font-size:26px;margin:0 0 4px}
p.sub{color:var(--muted);margin:0 0 16px}pre{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:12px 16px;overflow-x:auto;font-size:13px;line-height:1.45;white-space:pre-wrap}
.card{background:var(--card);border:1px solid var(--line);border-radius:8px;padding:12px 16px;margin:12px 0}.bad{color:var(--bad)}
button{font:inherit;font-weight:600;padding:8px 16px;border-radius:6px;border:1px solid var(--bad);background:transparent;color:var(--bad);cursor:pointer}
a{color:inherit}code{background:var(--code);padding:1px 5px;border-radius:4px;font-size:13px}table{border-collapse:collapse;font-size:14px}td,th{padding:4px 12px 4px 0;text-align:left}`

func writeE2EPage(w http.ResponseWriter) {
	var recent string
	if cfg, err := appconfig.Load(); err == nil {
		if db, err := bootstrap.OpenDB(cfg.DBPath); err == nil {
			recent = recentE2ERuns(db)
			_ = db.Close()
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>run_e2e_live</title><style>%s</style></head><body><main>
<h1>run_e2e_live</h1>
<p class="sub">Runs the whole daily pipeline once, in scheduler order: account_state, scan_market, stage_orders, submit_orders (twice), check_health, manage_exits, account_state, check_health. Each step is the real <code>/cron/&lt;job&gt;</code> route.</p>
<div class="card"><strong class="bad">This is a LIVE run.</strong> It stages, submits and manages <strong>real orders</strong> on your Schwab accounts, exactly like the scheduler. Nothing runs on this page load; only the button below starts it.</div>
<form method="post" action="/%s">
<input type="hidden" name="nonce" value="%s">
<p><label><input type="checkbox" name="oracle" value="1"> Also run the oracle: scan again over a scratch market database and compare signal counts (downloads market data; slow)</label></p>
<p><button type="submit">Run the live pipeline</button></p>
</form>
%s
<p class="sub">Output streams as it runs; closing the tab does not stop the run. See also <a href="/check_health">/check_health</a>.</p>
</main></body></html>`, pageCSS, run_e2e_live.JobName, pageNonces.issue(), recent)
}

func recentE2ERuns(db *sql.DB) string {
	rs, err := db.Query(`SELECT status, summary, finished_at FROM job_run WHERE job = ? ORDER BY id DESC LIMIT 5`, run_e2e_live.JobName)
	if err != nil {
		return ""
	}
	defer rs.Close()
	var b strings.Builder
	for rs.Next() {
		var status, summary, finished string
		if rs.Scan(&status, &summary, &finished) == nil {
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td><td>%s</td></tr>", html.EscapeString(finished), html.EscapeString(status), html.EscapeString(summary))
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return `<h3>Recent runs</h3><table><tr><th>Finished (UTC)</th><th>Result</th><th>Summary</th></tr>` + b.String() + `</table>`
}
