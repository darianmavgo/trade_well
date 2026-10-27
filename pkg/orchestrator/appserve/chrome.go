package appserve

import (
	"fmt"
	"html"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/run_e2e_live"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

// withChrome adds a navigation bar and a footer to every HTML page that GET serves, so each
// page links to the others and says which deploy it is from, without editing every template.
// The footer shows the deployed version and the strategy bundle compiled into it, which is how
// to tell whether the running version is the one you just deployed.
//
// Only GET and HEAD are wrapped (a POST streams, like /run_e2e_live), and /cron/, /_ah/ and
// API routes are left alone. A response that is not HTML, or has no <body>, passes through as
// it was.
func withChrome(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) ||
			strings.HasPrefix(p, "/cron/") || strings.HasPrefix(p, "/_ah/") || strings.HasPrefix(p, "/api/") || wantsJSON(r) {
			next.ServeHTTP(w, r)
			return
		}
		buf := run_e2e_live.NewBufferedResponse()
		next.ServeHTTP(buf, r)
		body := buf.Body()
		if strings.HasPrefix(buf.Header().Get("Content-Type"), "text/html") && buf.Status() < 400 {
			body = injectChrome(body)
		}
		for k, v := range buf.Header() {
			w.Header()[k] = v
		}
		w.Header().Del("Content-Length")
		w.WriteHeader(buf.Status())
		_, _ = w.Write([]byte(body))
	})
}

var (
	bodyOpen  = regexp.MustCompile(`(?i)<body[^>]*>`)
	bodyClose = regexp.MustCompile(`(?i)</body>`)
)

const chromeCSS = `<style>
.chrome-nav{display:flex;flex-wrap:wrap;gap:4px 14px;padding:8px 16px;background:#171b21;border-bottom:1px solid #2a313a;font:13px -apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif}
.chrome-nav a{color:#9ecbff;text-decoration:none}.chrome-nav a:hover{text-decoration:underline}
.chrome-foot{margin:32px 0 0;padding:10px 16px;border-top:1px solid #2a313a;color:#98a2b0;font:12px ui-monospace,Menlo,monospace}
</style>`

// navLinks are the operator pages, in the order a day usually goes.
var navLinks = [][2]string{
	{"/ops", "ops"}, {"/ready", "ready"}, {"/scan", "scan"}, {"/check_health", "health"},
	{"/account_state", "accounts"}, {"/stage_orders", "stage"}, {"/manage_exit", "exits"},
	{"/strategy", "strategies"}, {"/config", "config"}, {"/allowlist", "allowlist"}, {"/env", "env"}, {"/run_e2e_live", "e2e"}, {"/killswitch", "killswitch"}, {"/", "tables"},
}

func injectChrome(page string) string {
	open := bodyOpen.FindStringIndex(page)
	if open == nil {
		return page
	}
	var nav strings.Builder
	nav.WriteString(chromeCSS + `<nav class="chrome-nav">`)
	for _, l := range navLinks {
		fmt.Fprintf(&nav, `<a href="%s">%s</a>`, l[0], l[1])
	}
	nav.WriteString(`</nav>`)
	page = page[:open[1]] + nav.String() + page[open[1]:]
	if idx := bodyClose.FindStringIndex(page); idx != nil {
		page = page[:idx[0]] + footerHTML() + page[idx[0]:]
	} else {
		page += footerHTML()
	}
	return page
}

func footerHTML() string {
	version := os.Getenv("GAE_VERSION")
	if version == "" {
		version = "local"
	}
	service := os.Getenv("GAE_SERVICE")
	if service == "" {
		service = "-"
	}
	bundle := "bundle unreadable"
	if b, err := strategies.ReadBundledStrategyAssetInfo(); err == nil {
		bundle = "bundle " + b.Hash + " (" + strconv.Itoa(len(b.IDs)) + " rows)"
	}
	return `<div class="chrome-foot">` + html.EscapeString(fmt.Sprintf("version %s · service %s · %s · allowlist %s · rendered %s UTC",
		version, service, bundle, os.Getenv(strategies.EnvAllowlist), time.Now().UTC().Format("2006-01-02 15:04:05"))) + `</div>`
}
