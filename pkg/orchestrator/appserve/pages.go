package appserve

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/bootstrap"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
)

// Operator pages (/ops, /ready, /scan, /config, /env) share one layout. Each is HTML by
// default and returns the same data as JSON with ?format=json (Rule 5): one handler, one data
// function, two renderings.

type pageData struct {
	Title string
	Sub   string
	D     any
	Email bool // render for an email client: literal colours, no dark mode (see emailCSS)
}

var pageFuncs = template.FuncMap{
	"mul100":   func(f float64) float64 { return f * 100 },
	"css":      func() template.CSS { return template.CSS(dashboard.PageStyle() + pageExtraCSS) },
	"emailcss": func() template.CSS { return template.CSS(emailCSS(dashboard.PageStyle() + pageExtraCSS)) },
	"tag": func(s string) string {
		switch strings.ToLower(s) {
		case "ok", "released", "resolves":
			return "ok"
		case "warn", "empty", "not_due":
			return "warn"
		}
		return "bad"
	},
}

const pageExtraCSS = `
  form.inline { display:inline; margin:0; }
  button, input[type=submit] { font:inherit; padding:2px 10px; border-radius:6px; border:1px solid var(--line); background:var(--card); color:var(--fg); cursor:pointer; }
  button.live { border-color:var(--bad); color:var(--bad); }
  input[type=text] { font:inherit; padding:2px 6px; border:1px solid var(--line); border-radius:6px; background:var(--bg); color:var(--fg); }
  td.sum { max-width:420px; overflow-wrap:anywhere; }
`

const pageStart = `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>{{.Title}}</title><style>{{if .Email}}{{emailcss}}{{else}}{{css}}{{end}}</style></head><body><main><h1>{{.Title}}</h1><p class="sub">{{.Sub}}</p>`
const pageEnd = `</main></body></html>`

// newPage parses a page body into the shared layout.
func newPage(name, body string) *template.Template {
	return template.Must(template.New(name).Funcs(pageFuncs).Parse(pageStart + body + pageEnd))
}

func wantsJSON(r *http.Request) bool { return r.URL.Query().Get("format") == "json" }

// writePage renders tmpl, or data as JSON when the request asks for it.
func writePage(w http.ResponseWriter, r *http.Request, tmpl *template.Template, title, sub string, data any) {
	w.Header().Set("Cache-Control", "no-store")
	if wantsJSON(r) {
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(data)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, pageData{Title: title, Sub: sub, D: data}); err != nil {
		log.Printf("[appserve] page %s: %v", title, err)
	}
}

// openPageDB opens the orchestrator database for a read-only page.
func openPageDB() (*appconfig.Config, *sql.DB, error) {
	cfg, err := appconfig.Load()
	if err != nil {
		return nil, nil, err
	}
	db, err := bootstrap.OpenDB(cfg.DBPath)
	if err != nil {
		return cfg, nil, err
	}
	return cfg, db, nil
}
