package appserve

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/configview"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
)

var configPage = newPage("config", `
{{with .D}}
<div class="card">Object <code>{{.Object}}</code> &middot; bundle <code>{{.Bundle.Hash}}</code> holds {{len .Bundle.IDs}} strategy row(s) &middot; <a href="/ready">readiness</a> &middot; <a href="/env">running environment</a></div>
<h2>STRATEGY_ALLOWLIST</h2>
<p><code>{{.Allowlist}}</code></p>
<table><tr><th>Entry</th><th>Resolves</th><th>Members / problem</th></tr>
{{range .Entries}}<tr><td>{{.Entry}}</td><td><span class="tag {{if .OK}}ok{{else}}bad{{end}}">{{if .OK}}yes{{else}}no{{end}}</span></td><td class="sum">{{if .OK}}{{range .Members}}{{.}} {{end}}{{else}}{{.Problem}}{{end}}</td></tr>{{end}}
</table>
<h2>Try a change before saving it</h2>
<form method="get" action="/config"><input type="text" name="try" size="70" value="{{.Candidate}}" placeholder="a+b+c,other-strategy"> <button>Check</button></form>
{{if .Trial}}<table><tr><th>Entry</th><th>Resolves</th><th>Members / problem</th></tr>
{{range .Trial}}<tr><td>{{.Entry}}</td><td><span class="tag {{if .OK}}ok{{else}}bad{{end}}">{{if .OK}}yes{{else}}no{{end}}</span></td><td class="sum">{{if .OK}}{{range .Members}}{{.}} {{end}}{{else}}{{.Problem}}{{end}}</td></tr>{{end}}
</table>{{end}}
<p class="muted">Checking changes nothing. To save a list: <a href="/allowlist">edit it at /allowlist</a>, or <code>deploy_gae/config.sh set STRATEGY_ALLOWLIST=…</code> or <code>orchestrator strategy_allowlist set …</code>, after <code>scripts/export_strategies.sh</code> if a new row is involved.</p>
<h2>Variables</h2>
<div class="table-wrap"><table><tr><th>Name</th><th>Value</th></tr>
{{range .Vars}}<tr><td><code>{{.Name}}</code></td><td class="{{if .Secret}}muted{{end}}">{{.Value}}</td></tr>{{end}}
</table></div>
{{end}}`)

// registerConfigPage mounts /config behind the dashboard's Basic Auth: the config object with
// secrets hidden, the allowlist, and a way to try an allowlist before saving it.
func registerConfigPage(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(handleConfigPage))
	if err != nil {
		return fmt.Errorf("/config: %w", err)
	}
	root.Handle("/config", h)
	return nil
}

func handleConfigPage(w http.ResponseWriter, r *http.Request) {
	try, _ := url.QueryUnescape(r.URL.Query().Get("try"))
	v, err := configview.Load(r.Context(), try)
	if err != nil {
		http.Error(w, "config: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writePage(w, r, configPage, "Config", "The configuration object, with secrets hidden.", v)
}
