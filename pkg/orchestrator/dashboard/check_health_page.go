package dashboard

import (
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/check_health"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
)

// handleCheckHealth serves /check_health: which scheduled job did its job, and
// what the last health run found. Same job name as the CLI subcommand and
// /cron/check_health, same Basic Auth as the rest of the dashboard (it is
// mounted in Handler's mux). It only reads stored rows: it never calls Schwab
// and never writes, so reloading the page has no side effects. A fresh health
// run is `orchestrator cron check_health`.
func (s *Server) handleCheckHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDB(); err != nil {
		log.Printf("[check_health] page: database unavailable: %v", err)
		http.Error(w, "ERROR: database unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}
	st, err := check_health.LoadStatus(s.db, time.Now().UTC())
	if err != nil {
		log.Printf("[check_health] page: %v", err)
		http.Error(w, "ERROR: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := checkHealthTmpl.Execute(w, newCheckHealthView(st)); err != nil {
		log.Printf("[check_health] page render: %v", err)
	}
}

type jobView struct {
	Job, Slot, Verdict, Class, LastRun, LastStatus, Summary, Detail string
}

type checkHealthView struct {
	Updated   string
	Healthy   bool
	Root      string
	Jobs      []jobView
	Audits    []check_health.Audit
	Positions []check_health.PositionAudit
	Findings  []check_health.Finding
	HasAudit  bool
}

func newCheckHealthView(st *check_health.Status) checkHealthView {
	loc := job_runs.EasternTime()
	v := checkHealthView{
		Updated:   st.At.In(loc).Format("Mon 2006-01-02 15:04:05 MST"),
		Healthy:   st.Healthy(),
		Audits:    st.Audits,
		Positions: st.Positions,
		Findings:  st.Findings,
		HasAudit:  len(st.Audits) > 0,
	}
	if st.Root != nil {
		v.Root = st.Root.Detail
	}
	for _, j := range st.Jobs {
		jv := jobView{Job: j.Job, Slot: j.Slot, Verdict: j.Verdict, Detail: j.Detail, Class: "warn"}
		switch j.Verdict {
		case check_health.VerdictOK:
			jv.Class = "ok"
		case check_health.VerdictNeverRan, check_health.VerdictMissed, check_health.VerdictFailed:
			jv.Class = "bad"
		}
		if jv.Slot == "" {
			jv.Slot = "-"
		}
		if j.Last != nil {
			jv.LastRun = j.Last.FinishedAt.In(loc).Format("Mon 01-02 15:04:05")
			jv.LastStatus = j.Last.Status
			jv.Summary = j.Last.Summary
		} else {
			jv.LastRun = "never"
		}
		v.Jobs = append(v.Jobs, jv)
	}
	return v
}

var checkHealthTmpl = template.Must(template.New("check_health").Funcs(template.FuncMap{
	"css": func() template.CSS { return template.CSS(pageStyle) },
	"et":  func(t time.Time) string { return t.In(job_runs.EasternTime()).Format("Mon 01-02 15:04") },
	"sev": func(s string) string {
		if s == "CRITICAL" || s == "ACTION_REQUIRED" {
			return "bad"
		}
		if s == "HEALTHY" {
			return "ok"
		}
		return "warn"
	},
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>check_health</title>
<style>{{css}}</style>
</head>
<body>
<main>
  <h1>check_health</h1>
  <p class="sub">Updated {{.Updated}}. <a href="/check_health">Reload</a> &middot; <a href="/">Dashboard</a>. This page only reads stored results; the button runs a fresh health check (read-only against Schwab; it stores the result and emails the report).</p>
  <form method="post" action="/check_health/run"><button type="submit">Run check_health now</button></form>

  {{if .Healthy}}
  <div class="card"><strong class="ok">Every scheduled job that is due recorded a run for its latest slot.</strong></div>
  {{else}}
  <div class="card"><strong class="bad">A scheduled job did not do its job.</strong><br>{{.Root}}</div>
  {{end}}

  <h2>Current positions</h2>
  {{if .Positions}}
  <p class="sub">Each held position against the books and its strategy: accounting, strategy, take-profit, stop-loss, hold window and exit date. Worst first.</p>
  <div class="table-wrap">
  <table>
    <tr><th>Status</th><th>Account</th><th>Symbol</th><th>Qty</th><th>Strategy</th><th>Entry order</th><th>Filled (ET)</th><th>Exit date</th><th>Strategy says</th><th>Take-profit</th><th>Stop-loss</th><th>Problems</th></tr>
    {{range .Positions}}
    <tr>
      <td><span class="tag {{sev .Status}}">{{.Status}}</span></td>
      <td>{{.AccountNumber}}</td><td>{{.Symbol}}</td><td>{{printf "%.0f" .Quantity}}</td>
      <td>{{if .Strategy}}{{.Strategy}}{{else}}<span class="bad">unknown</span>{{end}}</td>
      <td>{{if .EntryOrderID}}{{.EntryOrderID}}{{else}}-{{end}}</td>
      <td>{{if .EntryFilledAt}}{{.EntryFilledAt}}{{else}}-{{end}}</td>
      <td>{{if .ExitDate}}{{.ExitDate}} <span class="muted">({{.ExitDateSrc}})</span>{{else}}<span class="bad">none</span>{{end}}</td>
      <td>{{if .ExpectedExit}}{{.ExpectedExit}}{{else}}-{{end}}</td>
      <td>{{if .TakeProfit}}${{printf "%.2f" .TakeProfit}}{{else}}<span class="bad">none</span>{{end}}{{if .ExpectedTP}} <span class="muted">/ ${{printf "%.2f" .ExpectedTP}}</span>{{end}}</td>
      <td>{{if .StopLoss}}${{printf "%.2f" .StopLoss}}{{else}}<span class="bad">none</span>{{end}}{{if .ExpectedStop}} <span class="muted">/ ${{printf "%.2f" .ExpectedStop}}</span>{{end}}</td>
      <td>{{range .Issues}}{{.}}<br>{{end}}</td>
    </tr>
    {{end}}
  </table>
  </div>
  {{else}}
  <p class="muted">No positions recorded by the last health run (or it has not run on this database).</p>
  {{end}}

  <h2>Scheduled jobs</h2>
  <div class="table-wrap">
  <table>
    <tr><th>Job</th><th>Latest slot (ET)</th><th>Verdict</th><th>Last recorded run (ET)</th><th>Run status</th><th>Summary</th></tr>
    {{range .Jobs}}
    <tr>
      <td><code>{{.Job}}</code></td>
      <td>{{.Slot}}</td>
      <td><span class="tag {{.Class}}">{{.Verdict}}</span></td>
      <td>{{.LastRun}}</td>
      <td>{{.LastStatus}}</td>
      <td>{{.Summary}}</td>
    </tr>
    {{if .Detail}}<tr><td></td><td colspan="5" class="muted">{{.Detail}}</td></tr>{{end}}
    {{end}}
  </table>
  </div>

  <h2>Latest audit per account</h2>
  {{if .HasAudit}}
  <div class="table-wrap">
  <table>
    <tr><th>Account</th><th>When (ET)</th><th>Status</th><th>Positions</th><th>Protected</th><th>Unprotected</th><th>Orphan orders</th><th>Critical</th><th>Warnings</th></tr>
    {{range .Audits}}
    <tr>
      <td>{{.AccountNumber}}</td><td>{{et .Timestamp}}</td>
      <td><span class="tag {{sev .Status}}">{{.Status}}</span></td>
      <td>{{.TotalPositions}}</td><td>{{.ProtectedPositions}}</td><td>{{.UnprotectedPositions}}</td>
      <td>{{.OrphanOrders}}</td><td>{{.CriticalFindings}}</td><td>{{.WarningFindings}}</td>
    </tr>
    {{end}}
  </table>
  </div>
  {{else}}
  <p class="muted">No audit recorded yet. <code>check_health</code> has not completed a run on this database.</p>
  {{end}}

  <h2>Findings from the latest run per account</h2>
  {{if .Findings}}
  <div class="table-wrap">
  <table>
    <tr><th>Account</th><th>Severity</th><th>Check</th><th>Category</th><th>Subject</th><th>Detail</th><th>Run (ET)</th></tr>
    {{range .Findings}}
    <tr>
      <td>{{.AccountNumber}}</td>
      <td><span class="tag {{sev .Severity}}">{{.Severity}}</span></td>
      <td><code>{{.CheckName}}</code></td><td>{{.Category}}</td><td>{{.Subject}}</td><td>{{.Detail}}</td><td>{{et .RunAt}}</td>
    </tr>
    {{end}}
  </table>
  </div>
  {{else}}
  <p class="muted">No findings stored.</p>
  {{end}}
</main>
</body>
</html>
`))
