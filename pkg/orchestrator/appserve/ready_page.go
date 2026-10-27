package appserve

import (
	"fmt"
	"net/http"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/ready"
)

var readyPage = newPage("ready", `
{{with .D}}
<div class="card">Overall <span class="tag {{tag .Status}}">{{.Status}}</span> &middot; checked {{.GeneratedAt.Format "Mon 2006-01-02 15:04:05 UTC"}}
 &middot; <a href="/ops">ops</a> &middot; <a href="/config">config</a></div>
<div class="table-wrap"><table>
<tr><th>Check</th><th>Status</th><th>Detail</th></tr>
{{range .Checks}}<tr><td><b>{{.Name}}</b></td><td><span class="tag {{tag .Status}}">{{.Status}}</span></td><td class="sum">{{.Detail}}</td></tr>{{end}}
</table></div>
<p class="muted">The same checks run at 09:00 and 17:30 ET and email you only when something is not ok. <code>?format=json</code> returns them; add <code>&amp;strict=1</code> to get HTTP 503 when the status is bad (deploy.sh uses that).</p>
{{end}}`)

// registerReadyPage mounts /ready behind the dashboard's Basic Auth.
func registerReadyPage(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(handleReadyPage))
	if err != nil {
		return fmt.Errorf("/ready: %w", err)
	}
	root.Handle("/ready", h)
	return nil
}

func handleReadyPage(w http.ResponseWriter, r *http.Request) {
	cfg, db, err := openPageDB()
	if err != nil {
		http.Error(w, "database: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()
	rep := ready.Run(r.Context(), ready.Options{
		DB: db, Now: time.Now(), Allowlist: cfg.StrategyAllowlist, MarketDB: cfg.MarketDB, TokenPath: cfg.Schwab.TokenPath,
	})
	if r.URL.Query().Get("strict") == "1" && rep.Status == ready.Bad {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	writePage(w, r, readyPage, "Ready", "Will tonight's scan and tomorrow's orders work?", rep)
}
