package appserve

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

var scanPage = newPage("scan", `
{{with .D}}
<div class="card">Last {{.Days}} day(s), {{if .All}}<b>every strategy ever scanned</b> &middot; <a href="?days={{.Days}}">only the deployed ones</a>{{else}}<b>the deployed strategies</b> ({{range $i, $s := .Deployed}}{{if $i}}, {{end}}<a href="/status/{{$s}}">{{$s}}</a>{{end}}){{if .Hidden}} &middot; {{.Hidden}} row(s) of other strategies hidden &middot; <a href="?days={{.Days}}&amp;all=1">show them</a>{{end}}{{end}}:
 <b>{{len .Buys}}</b> buy candidate(s), <b>{{len .NoBuys}}</b> no-buy note(s)
 &middot; <a href="?days=7{{if .All}}&amp;all=1{{end}}">7d</a> <a href="?days=30{{if .All}}&amp;all=1{{end}}">30d</a> &middot; <a href="/ops">ops</a> &middot; <a href="/stage_orders">stage preview</a></div>
{{if and (not .All) (not .Buys) (not .NoBuys)}}<div class="card"><span class="tag warn">nothing yet</span> No scan has recorded a result for the deployed strategies in this window. The evening scan writes them at 18:00 ET: check that it ran on <a href="/ops">/ops</a>.</div>{{end}}
<h2>Why, per strategy</h2>
{{if .Trace}}<div class="table-wrap"><table>
<tr><th>Bar</th><th>Strategy</th><th>Verdict</th><th>Reason</th><th>Numbers</th></tr>
{{range .Trace}}<tr><td>{{.Date}}</td><td><a href="/status/{{.Strategy}}">{{.Strategy}}</a></td><td><span class="tag {{if .Entry}}ok{{else}}warn{{end}}">{{if .Entry}}ENTER{{else}}no entry{{end}}</span></td><td class="sum">{{.Reason}}</td><td class="muted">{{.FactsText}}</td></tr>{{end}}
</table></div>{{else}}<p class="muted">No scan has recorded its reasons in this window yet. Each scan from now on stores why every strategy did or did not enter.</p>{{end}}
{{if .Latest}}<h2>Latest scan, last sessions</h2><div class="table-wrap"><table>
<tr><th>Bar</th><th>Strategy</th><th>Verdict</th><th>Reason</th><th>Numbers</th></tr>
{{range .Latest}}<tr><td>{{.Date}}</td><td><a href="/status/{{.Strategy}}">{{.Strategy}}</a></td><td><span class="tag {{if .Entry}}ok{{else}}warn{{end}}">{{if .Entry}}ENTER{{else}}no entry{{end}}</span></td><td class="sum">{{.Reason}}</td><td class="muted">{{.FactsText}}</td></tr>{{end}}
</table></div>{{end}}
<h2>Buy candidates</h2>
{{if .Buys}}<div class="table-wrap"><table>
<tr><th>Bar</th><th>For session</th><th>Strategy</th><th>Symbol</th><th>Close</th><th>Buy limit</th><th>Take profit</th><th>Alloc</th><th>Hold</th><th>Status</th></tr>
{{range .Buys}}<tr><td>{{.AsOf}}</td><td>{{.NextSession}}</td><td><a href="/status/{{.StrategyID}}">{{.StrategyID}}</a>{{if and .StackID (ne .StackID .StrategyID)}}<br><span class="muted">in {{.StackID}}</span>{{end}}</td><td>{{.Symbol}}</td>
<td>{{printf "%.2f" .Close}}</td><td>{{printf "%.2f" .BuyLimit}}</td><td>{{printf "%.2f" .TakeProfit}}</td><td>{{printf "%.0f" (mul100 .AllocPct)}}%</td><td>{{.HoldDays}}d</td><td><span class="tag {{tag .Status}}">{{.Status}}</span></td></tr>{{end}}
</table></div>{{else}}<p class="muted">No buy candidates in this window.</p>{{end}}
<h2>No buy, and why</h2>
{{if .NoBuys}}<div class="table-wrap"><table>
<tr><th>Bar</th><th>Strategy</th><th>Reason</th></tr>
{{range .NoBuys}}<tr><td>{{.Date}}</td><td><a href="/status/{{.Strategy}}">{{.Strategy}}</a></td><td class="sum">{{.Detail}}</td></tr>{{end}}
</table></div>{{else}}<p class="muted">No scan has recorded a no-buy note in this window.</p>{{end}}
{{end}}`)

// registerScanPage mounts /scan behind the dashboard's Basic Auth.
func registerScanPage(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(handleScanPage))
	if err != nil {
		return fmt.Errorf("/scan: %w", err)
	}
	root.Handle("/scan", h)
	return nil
}

type scanView struct {
	scan_market.Results
	All      bool     `json:"all"`
	Deployed []string `json:"deployed"`
}

func handleScanPage(w http.ResponseWriter, r *http.Request) {
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 365 {
			http.Error(w, "days must be an integer from 1 to 365", http.StatusBadRequest)
			return
		}
		days = n
	}
	_, db, err := openPageDB()
	if err != nil {
		http.Error(w, "database: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer db.Close()
	// By default only the strategies in the live allowlist: the table also holds the history of
	// strategies deployed before, which buries the ones that matter. ?all=1 shows everything.
	cfg, err := appconfig.Load()
	if err != nil {
		http.Error(w, "config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	all := r.URL.Query().Get("all") == "1"
	deployed := strategies.Members(cfg.StrategyAllowlist)
	filter := deployed
	if all {
		filter = nil
	}
	res, err := scan_market.RecentFor(db, time.Now(), days, filter)
	if err != nil {
		http.Error(w, "scan results: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writePage(w, r, scanPage, "Scan results", "What the evening scan found, per deployed strategy, and why it did not buy.",
		scanView{Results: res, All: all, Deployed: deployed})
}
