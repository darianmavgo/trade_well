package appserve

import (
	"database/sql"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/killswitch"
)

// currentKillswitch reads the persisted switch for a cron request. A database that
// does not exist yet cannot have had it engaged. Any error reading it is logged and
// treated as OFF: the jobs need that same database, so a failure here is theirs to report.
func currentKillswitch() killswitch.State {
	cfg, err := appconfig.Load()
	if err != nil {
		log.Printf("[%s] cannot read config: %v", killswitch.JobName, err)
		return killswitch.State{}
	}
	db, err := appconfig.OpenDB(cfg.DBPath)
	if err != nil {
		log.Printf("[%s] cannot open the database: %v", killswitch.JobName, err)
		return killswitch.State{}
	}
	defer db.Close()
	st, err := killswitch.Current(db)
	if err != nil {
		log.Printf("[%s] cannot read the switch: %v", killswitch.JobName, err)
		return killswitch.State{}
	}
	return st
}

// registerKillswitch mounts /killswitch behind the dashboard's Basic Auth. GET shows
// the state; a POST with the page's one-time token engages or releases it.
func registerKillswitch(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(handleKillswitch))
	if err != nil {
		return fmt.Errorf("/%s: %w", killswitch.JobName, err)
	}
	root.Handle("/"+killswitch.JobName, h)
	return nil
}

func handleKillswitch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeKillswitchPage(w, "")
	case http.MethodPost:
		changeKillswitch(w, r)
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

func changeKillswitch(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	action := r.PostFormValue("action")
	if action != "engage" && action != "release" {
		http.Error(w, "action must be engage or release", http.StatusBadRequest)
		return
	}
	if !pageNonces.consume(r.PostFormValue("nonce")) {
		http.Error(w, "missing, expired or used token: reload /"+killswitch.JobName+" and try again", http.StatusForbidden)
		return
	}
	cfg, err := appconfig.Load()
	if err != nil {
		http.Error(w, "config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	db, err := appconfig.OpenDB(cfg.DBPath)
	if err != nil {
		http.Error(w, "database: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	user, _, _ := r.BasicAuth()
	reason := strings.TrimSpace(r.PostFormValue("reason"))
	var msg string
	if action == "engage" {
		// Flag first, so nothing new can start; then cancel what is already running.
		if _, err := killswitch.Set(db, true, user, reason, time.Now()); err != nil {
			http.Error(w, "could not engage: "+err.Error(), http.StatusInternalServerError)
			return
		}
		killed := killswitch.KillAll()
		log.Printf("[%s] ENGAGED by %s reason=%q killed=%v", killswitch.JobName, user, reason, killed)
		msg = fmt.Sprintf("Killswitch ENGAGED. Cron is off and %d running execution(s) were cancelled", len(killed))
		if len(killed) > 0 {
			msg += ": " + strings.Join(killed, ", ")
		}
		msg += ". Positions are untouched: use /exit_market to sell."
	} else {
		if _, err := killswitch.Set(db, false, user, reason, time.Now()); err != nil {
			http.Error(w, "could not release: "+err.Error(), http.StatusInternalServerError)
			return
		}
		log.Printf("[%s] RELEASED by %s reason=%q", killswitch.JobName, user, reason)
		msg = "Killswitch RELEASED. Cron jobs run again from the next scheduled slot."
	}
	writeKillswitchPage(w, msg)
}

func writeKillswitchPage(w http.ResponseWriter, notice string) {
	var st killswitch.State
	var lastRuns string
	if cfg, err := appconfig.Load(); err == nil {
		if db, err := appconfig.OpenDB(cfg.DBPath); err == nil {
			st, _ = killswitch.Current(db)
			lastRuns = recentJobRuns(db)
			_ = db.Close()
		}
	}
	running := killswitch.Running()
	var runList string
	if len(running) == 0 {
		runList = "<p class=\"sub\">No job is running on this instance right now.</p>"
	} else {
		var b strings.Builder
		b.WriteString("<ul>")
		for _, e := range running {
			fmt.Fprintf(&b, "<li><code>%s</code> since %s</li>", html.EscapeString(e.Name), e.Started.Format("15:04:05 UTC"))
		}
		b.WriteString("</ul>")
		runList = b.String()
	}
	banner, form := "", ""
	nonce := pageNonces.issue()
	if st.Engaged {
		banner = `<div class="card"><strong class="bad">KILLSWITCH IS ENGAGED.</strong> ` + html.EscapeString(st.String()) + `. Every <code>/cron/*</code> route is refused, including manual triggers and <code>/run_e2e_live</code>.</div>`
		form = `<form method="post" action="/` + killswitch.JobName + `"><input type="hidden" name="nonce" value="` + nonce + `"><input type="hidden" name="action" value="release">` +
			`<p><label>Reason (optional): <input type="text" name="reason" size="40"></label></p><p><button type="submit" class="ok">Release: turn cron back on</button></p></form>`
	} else {
		banner = `<div class="card"><strong>Killswitch is off.</strong> Cron jobs run on schedule.</div>`
		form = `<form method="post" action="/` + killswitch.JobName + `"><input type="hidden" name="nonce" value="` + nonce + `"><input type="hidden" name="action" value="engage">` +
			`<p><label>Reason (optional): <input type="text" name="reason" size="40"></label></p>` +
			`<p><button type="submit">ENGAGE: turn off cron and kill running jobs</button></p></form>` +
			`<p class="sub">Engaging refuses every <code>/cron/*</code> route (the scheduler and manual triggers) and cancels the jobs running now, mid broker call. It does not sell anything.</p>`
	}
	if notice != "" {
		notice = `<div class="card">` + html.EscapeString(notice) + `</div>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>killswitch</title><style>%s .ok{color:var(--ok);border-color:var(--ok)}:root{--ok:#1a7f37}</style></head><body><main>
<h1>killswitch</h1><p class="sub">Emergency stop for the scheduled pipeline. Also see <a href="/check_health">/check_health</a>.</p>
%s%s<h3>Running now</h3>%s<h3>Change</h3>%s%s
</main></body></html>`, pageCSS, notice, banner, runList, form, lastRuns)
}

func recentJobRuns(db *sql.DB) string {
	rs, err := db.Query(`SELECT job, status, finished_at FROM job_run ORDER BY id DESC LIMIT 8`)
	if err != nil {
		return ""
	}
	defer rs.Close()
	var b strings.Builder
	for rs.Next() {
		var job, status, at string
		if rs.Scan(&job, &status, &at) == nil {
			fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td><td>%s</td></tr>", html.EscapeString(job), html.EscapeString(status), html.EscapeString(at))
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return `<h3>Latest job runs</h3><table><tr><th>Job</th><th>Result</th><th>Finished (UTC)</th></tr>` + b.String() + `</table>`
}
