package appserve

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/ops"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/run_e2e_live"
)

var opsPage = newPage("ops", `
{{with .D}}
<div class="card">Board <span class="tag {{tag .Status}}">{{.Status}}</span>
 &middot; killswitch <span class="tag {{tag .Killswitch}}">{{.Killswitch}}</span>
 &middot; <a href="/ready">readiness</a> &middot; <a href="/scan">scan results</a> &middot; <a href="/check_health">health</a></div>
<div class="table-wrap"><table>
<tr><th>Job</th><th>Latest slot</th><th>Last run</th><th>Took</th><th>Next slot (ET)</th><th>What it said</th><th>Run now</th></tr>
{{range .Rows}}<tr>
<td><b>{{.Job}}</b>{{if .Live}} <span class="tag bad" title="can place or change real orders">live</span>{{end}}</td>
<td><span class="tag {{.Severity}}">{{.Verdict}}</span><br><span class="muted">{{.Slot}}</span></td>
<td>{{.Last}}<br><span class="muted">{{.LastAt}}</span></td>
<td>{{.Took}}</td>
<td>{{.Next}}</td>
<td class="sum">{{if .Detail}}{{.Detail}}{{else}}{{.Summary}}{{end}}</td>
<td>
{{if .DryRun}}<form class="inline" method="post" action="/ops/run"><input type="hidden" name="nonce" value="{{$.D.Nonce}}"><input type="hidden" name="job" value="{{.Job}}"><input type="hidden" name="mode" value="dry"><button>Dry run</button></form>{{end}}
{{if .Live}}<form class="inline" method="post" action="/ops/run"><input type="hidden" name="nonce" value="{{$.D.Nonce}}"><input type="hidden" name="job" value="{{.Job}}"><input type="hidden" name="mode" value="live"><input type="text" name="confirm" size="12" placeholder="type {{.Job}}" autocomplete="off"> <button class="live">Run LIVE</button></form>
{{else}}<form class="inline" method="post" action="/ops/run"><input type="hidden" name="nonce" value="{{$.D.Nonce}}"><input type="hidden" name="job" value="{{.Job}}"><input type="hidden" name="mode" value="run"><button>Run</button></form>{{end}}
</td></tr>{{end}}
</table></div>
<p class="muted">Times are Eastern. A live job places, changes or cancels real orders: it runs for real only when you type its name. Dry run is the CLI's <code>--dry-run</code> (<code>scan_market</code> still records its signals). Add <code>?format=json</code> for the data.</p>
{{end}}`)

type opsView struct {
	ops.Board
	Nonce string `json:"-"`
}

// registerOps mounts /ops behind the dashboard's Basic Auth: the board of every cron job, and
// POST /ops/run, which runs one through the same /cron handler a schedule uses.
func registerOps(root *http.ServeMux) error {
	board, err := dashboard.RequireAuth(http.HandlerFunc(handleOps))
	if err != nil {
		return fmt.Errorf("/ops: %w", err)
	}
	run, err := dashboard.RequireAuth(http.HandlerFunc(handleOpsRun))
	if err != nil {
		return fmt.Errorf("/ops/run: %w", err)
	}
	root.Handle("/ops", board)
	root.Handle("/ops/run", run)
	return nil
}

func handleOps(w http.ResponseWriter, r *http.Request) {
	_, db, err := openPageDB()
	if err != nil {
		http.Error(w, "database: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()
	b, err := ops.Build(db, time.Now())
	if err != nil {
		http.Error(w, "ops: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writePage(w, r, opsPage, "Ops", "Every cron job: its latest slot, how it went, and the next one.", opsView{Board: b, Nonce: pageNonces.issue()})
}

var opsResultPage = newPage("ops_result", `{{with .D}}
<div class="card"><b>{{.Job}}</b> ({{.Mode}}) returned <span class="tag {{.Tag}}">HTTP {{.Status}}</span> in {{.Took}}</div>
<pre>{{.Body}}</pre>
<p><a href="/ops">Back to ops</a></p>{{end}}`)

func handleOpsRun(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		http.Error(w, "POST only (use the buttons on /ops)", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if !pageNonces.consume(r.PostFormValue("nonce")) {
		http.Error(w, "missing, used or expired token: reload /ops and press the button again", http.StatusForbidden)
		return
	}
	job, mode := r.PostFormValue("job"), r.PostFormValue("mode")
	known := false
	for _, n := range CronJobNames() {
		known = known || n == job
	}
	if !known {
		http.Error(w, "unknown job", http.StatusBadRequest)
		return
	}
	query := ""
	switch mode {
	case "dry":
		if !ops.CanDryRun(job) {
			http.Error(w, job+" has no dry run", http.StatusBadRequest)
			return
		}
		query = "?dry_run=1"
	case "run":
		if ops.IsLive(job) {
			http.Error(w, job+" can place real orders: use Run LIVE", http.StatusBadRequest)
			return
		}
	case "live":
		if !ops.IsLive(job) {
			http.Error(w, job+" is not a live job: use Run", http.StatusBadRequest)
			return
		}
		if r.PostFormValue("confirm") != job {
			http.Error(w, "type the job name ("+job+") in the box to run it for real", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "unknown mode", http.StatusBadRequest)
		return
	}

	// A closed tab must not stop a job halfway.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 13*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/cron/"+url.PathEscape(job)+query, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("X-Appengine-Cron", "true") // the same entry a schedule uses, so the killswitch and job_run still apply
	log.Printf("[ops] run %s mode=%s by an authenticated dashboard user", job, mode)
	resp := run_e2e_live.NewBufferedResponse()
	start := time.Now()
	CronHandler().ServeHTTP(resp, req)
	took := time.Since(start).Round(time.Millisecond)
	tag := "ok"
	if resp.Status() >= 400 {
		tag = "bad"
	}
	writePage(w, r, opsResultPage, "Ops: "+job, "Result of the run you started.", map[string]any{
		"Job": job, "Mode": mode, "Status": resp.Status(), "Tag": tag, "Took": took.String(), "Body": resp.Body(),
	})
}
