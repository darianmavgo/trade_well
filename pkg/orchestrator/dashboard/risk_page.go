package dashboard

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/risk"
)

// riskHistoryRows is how many recorded sessions the page lists.
const riskHistoryRows = 60

type riskRow struct {
	Account     string   `json:"account"`
	AssessedAt  string   `json:"assessed_at"`
	Session     string   `json:"session"`
	Equity      float64  `json:"equity"`
	Peak        float64  `json:"peak"`
	DrawdownPct float64  `json:"drawdown_pct"`
	DayPct      float64  `json:"day_change_pct"`
	GrossPct    float64  `json:"gross_pct"`
	ScalePct    float64  `json:"entry_size_pct"`
	Halted      bool     `json:"halt_entries"`
	Status      string   `json:"status"`
	Class       string   `json:"-"`
	Reasons     []string `json:"reasons"`
}

type riskView struct {
	Updated  string       `json:"updated"`
	Accounts []riskRow    `json:"accounts"`
	History  []risk.Point `json:"history"`
}

// handleRisk serves /risk: each account's equity, drawdown from its high-water mark, the last
// session's change, exposure, and what new entries may do. HTML by default, JSON on
// ?format=json. Read-only: the `risk` job writes it.
func (s *Server) handleRisk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDB(); err != nil {
		http.Error(w, "ERROR: database unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}
	states, err := risk.All(s.db)
	if err == nil {
		var hist []risk.Point
		hist, err = risk.History(s.db, riskHistoryRows)
		if err == nil {
			view := riskView{Updated: time.Now().UTC().Format("2006-01-02 15:04 MST"), History: hist}
			for _, st := range states {
				row := riskRow{
					Account: st.Account, AssessedAt: st.AssessedAt.UTC().Format("2006-01-02 15:04 MST"), Session: st.Session,
					Equity: st.Equity, Peak: st.Peak, DrawdownPct: st.Drawdown * 100, DayPct: st.DayChange * 100,
					GrossPct: st.Gross * 100, ScalePct: st.Scale * 100, Halted: st.HaltEntries, Status: st.String(), Reasons: st.Reasons,
				}
				switch {
				case st.HaltEntries:
					row.Class = "bad"
				case st.Scale < 1:
					row.Class = "warn"
				default:
					row.Class = "ok"
				}
				view.Accounts = append(view.Accounts, row)
			}
			if r.URL.Query().Get("format") == "json" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(view)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if err := riskTmpl.Execute(w, view); err != nil {
				log.Printf("[risk] page render: %v", err)
			}
			return
		}
	}
	log.Printf("[risk] page: %v", err)
	http.Error(w, fmt.Sprintf("ERROR: %v", err), http.StatusInternalServerError)
}

var riskTmpl = template.Must(template.New("risk").Funcs(template.FuncMap{
	"css": func() template.CSS { return template.CSS(pageStyle) },
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>risk</title>
<style>{{css}}</style>
</head>
<body>
<main>
  <h1>risk</h1>
  <p class="sub">Updated {{.Updated}}. <a href="/risk">Reload</a> &middot; <a href="/check_health">check_health</a> &middot; <a href="/">Dashboard</a> &middot; <a href="/risk?format=json">?format=json</a>. Read-only: written by the <code>risk</code> job after each account sync. It limits <b>new entries only</b>; exits and stops always run.</p>
  {{if .Accounts}}
  <div class="table-wrap">
  <table>
    <thead><tr><th>Account</th><th>Entries</th><th>Equity</th><th>Peak</th><th>Drawdown</th><th>Last session</th><th>Long exposure</th><th>Assessed</th></tr></thead>
    <tbody>
    {{range .Accounts}}
    <tr><td>{{.Account}}</td><td class="{{.Class}}">{{.Status}}</td><td>${{printf "%.2f" .Equity}}</td><td>${{printf "%.2f" .Peak}}</td><td>{{printf "%.1f" .DrawdownPct}}%</td><td>{{printf "%+.1f" .DayPct}}%</td><td>{{printf "%.0f" .GrossPct}}%</td><td>{{.AssessedAt}}</td></tr>
    {{end}}
    </tbody>
  </table>
  </div>
  {{else}}
  <p class="sub">No assessment yet. Run <code>orchestrator risk</code> after an <code>account_state</code> sync.</p>
  {{end}}
  {{if .History}}
  <h2>Recorded equity <span class="muted">({{len .History}} newest)</span></h2>
  <div class="table-wrap">
  <table>
    <thead><tr><th>Session</th><th>Account</th><th>Equity</th></tr></thead>
    <tbody>{{range .History}}<tr><td>{{.Session}}</td><td>{{.Account}}</td><td>${{printf "%.2f" .Equity}}</td></tr>{{end}}</tbody>
  </table>
  </div>
  {{end}}
</main>
</body>
</html>`))
