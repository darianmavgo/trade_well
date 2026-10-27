package appserve

import (
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/configview"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategy_allowlist"
)

// allowlistView is the /allowlist page: the stored list as text, every strategy this binary can
// resolve as a picker, and the outcome of a preview or save.
type allowlistView struct {
	Current []string                   `json:"current"`
	Text    string                     `json:"text"`
	Choices []configview.Choice        `json:"choices"`
	Change  *strategy_allowlist.Change `json:"change,omitempty"`
	Added   []string                   `json:"added,omitempty"`
	Removed []string                   `json:"removed,omitempty"`
	Saved   bool                       `json:"saved,omitempty"`
	Error   string                     `json:"error,omitempty"`
	Nonce   string                     `json:"-"`
}

var allowlistPage = newPage("allowlist", `
{{with .D}}
<style>textarea{font:inherit;width:100%;max-width:60em;padding:6px;border:1px solid var(--line);border-radius:6px;background:var(--bg);color:var(--fg)} .pick{max-height:28em;overflow:auto;border:1px solid var(--line);border-radius:6px;padding:4px 10px} .pick label{display:block;padding:1px 0}</style>
{{if .Error}}<div class="card"><span class="tag bad">not saved</span> {{.Error}}</div>{{end}}
{{if .Change}}<div class="card">{{if .Saved}}<span class="tag ok">saved</span> The config object now holds the list below. A running instance keeps the list it started with: <strong>redeploy</strong> to pick it up. The old object is kept at <code>config/env.prev</code>.{{else}}<span class="tag warn">preview</span> Nothing was written.{{end}}
{{if or .Added .Removed}}<br>{{range .Added}}<code>+ {{.}}</code> {{end}}{{range .Removed}}<code>&minus; {{.}}</code> {{end}}{{else}}<br>No change to the list.{{end}}</div>{{end}}
<div class="card">Stored in the config object; changing it does not change a running instance. Also see <a href="/config">/config</a> and <a href="/ready">/ready</a>. {{len .Current}} entr{{if eq (len .Current) 1}}y{{else}}ies{{end}} now.</div>

<h2>Edit as text</h2>
<form method="post" action="/allowlist">
<input type="hidden" name="nonce" value="{{.Nonce}}"><input type="hidden" name="mode" value="text">
<p><textarea name="list" rows="5" spellcheck="false" placeholder="a+b+c,other-strategy">{{.Text}}</textarea></p>
<p class="muted">Comma or line separated. <code>a+b</code> is a stack: the first member has priority for cash.</p>
<p><button name="do" value="preview">Preview</button> <button name="do" value="save" class="live">Save</button></p>
</form>

<h2>Pick from the list</h2>
<form method="post" action="/allowlist">
<input type="hidden" name="nonce" value="{{.Nonce}}"><input type="hidden" name="mode" value="pick">
<p><input type="text" id="flt" placeholder="filter" size="30" oninput="var q=this.value.toLowerCase();document.querySelectorAll('.pick label').forEach(function(l){l.style.display=l.textContent.toLowerCase().includes(q)?'':'none'})"></p>
<div class="pick">{{range .Choices}}<label><input type="checkbox" name="pick" value="{{.ID}}"{{if .Selected}} checked{{end}}{{if and (not .OK) (not .Selected)}} disabled{{end}}> <code>{{.ID}}</code>{{if .Name}} <span class="muted">{{.Name}}</span>{{end}}{{if not .OK}} <span class="tag bad">{{.Problem}}</span>{{end}}</label>{{end}}</div>
<p class="muted">A strategy that does not resolve from this binary's bundle cannot be picked: export it first (<code>scripts/export_strategies.sh</code>), then deploy.</p>
<p><button name="do" value="preview">Preview</button> <button name="do" value="save" class="live">Save</button></p>
</form>
{{end}}`)

// registerAllowlistPage mounts /allowlist behind the dashboard's Basic Auth. GET shows the stored
// list; a POST with the page's one-time token previews or saves a new one.
func registerAllowlistPage(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(handleAllowlist))
	if err != nil {
		return fmt.Errorf("/allowlist: %w", err)
	}
	root.Handle("/allowlist", h)
	return nil
}

func handleAllowlist(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		writeAllowlist(w, r, nil, "", "", http.StatusOK)
	case http.MethodPost:
		changeAllowlist(w, r)
	default:
		http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
	}
}

func changeAllowlist(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-site request refused", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	do, mode := r.PostFormValue("do"), r.PostFormValue("mode")
	if do != "preview" && do != "save" {
		http.Error(w, "do must be preview or save", http.StatusBadRequest)
		return
	}
	if mode != "text" && mode != "pick" {
		http.Error(w, "mode must be text or pick", http.StatusBadRequest)
		return
	}
	if !pageNonces.consume(r.PostFormValue("nonce")) {
		http.Error(w, "missing, expired or used token: reload /allowlist and try again", http.StatusForbidden)
		return
	}
	list := strings.Join(r.PostForm["pick"], ",")
	if mode == "text" {
		list = strings.NewReplacer("\r", ",", "\n", ",").Replace(r.PostFormValue("list"))
	}
	ch, err := strategy_allowlist.Update(r.Context(), "set", []string{list}, do != "save")
	if err != nil {
		writeAllowlist(w, r, nil, list, err.Error(), http.StatusBadRequest)
		return
	}
	user, _, _ := r.BasicAuth()
	if ch.Written {
		log.Printf("[strategy_allowlist] %s set %s -> %s", user, strings.Join(ch.Before, ","), strings.Join(ch.After, ","))
	}
	writeAllowlist(w, r, &ch, "", "", http.StatusOK)
}

// writeAllowlist builds the view from the stored list (or from ch, the list just saved) and
// renders it, HTML by default and JSON with ?format=json.
func writeAllowlist(w http.ResponseWriter, r *http.Request, ch *strategy_allowlist.Change, typed, failure string, status int) {
	current, err := strategy_allowlist.Current(r.Context())
	if err != nil {
		http.Error(w, "allowlist: "+err.Error(), http.StatusInternalServerError)
		return
	}
	v := allowlistView{Nonce: pageNonces.issue(), Error: failure}
	v.Current = current
	if ch != nil {
		v.Change, v.Saved = ch, ch.Written
		for _, id := range ch.After {
			if !slices.Contains(ch.Before, id) {
				v.Added = append(v.Added, id)
			}
		}
		for _, id := range ch.Before {
			if !slices.Contains(ch.After, id) {
				v.Removed = append(v.Removed, id)
			}
		}
		if !ch.Written {
			current = ch.After // a preview shows the list being tried
		}
	}
	v.Text = strings.Join(current, ",")
	v.Choices = configview.Choices(current)
	if typed != "" {
		v.Text = typed // keep what the operator typed when it was refused
	}
	if status != http.StatusOK {
		// writePage sets the same two headers, but they must come before the status.
		w.Header().Set("Cache-Control", "no-store")
		if wantsJSON(r) {
			w.Header().Set("Content-Type", "application/json")
		} else {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		w.WriteHeader(status)
	}
	writePage(w, r, allowlistPage, "Strategy allowlist", "Which strategies the jobs may scan, stage and trade.", v)
}
