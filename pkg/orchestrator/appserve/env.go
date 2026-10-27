package appserve

import (
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/dashboard"
)

// sensitiveKey matches variable names whose value must never be shown: passwords, tokens, API keys
// and secrets, client ids, logins and account numbers.
var sensitiveKey = regexp.MustCompile(`(?i)PASS|PWD|SECRET|TOKEN|KEY|CREDENTIAL|AUTH|CLIENT_ID|USERNAME|ACCOUNT`)

const maskedValue = "••••••••"

// platformKey matches the variables the platform and the shell set (App Engine, buildpacks,
// locale, PATH). They are real but are not the app's configuration, so /env hides them until
// you ask with ?all=1.
var platformKey = regexp.MustCompile(`^(GAE_|GOOGLE_|CNB_|S2A_|LC_|LANG|HOME$|PATH$|PWD$|PORT$|HOSTNAME$|SHLVL$|_$)`)

type envEntry struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Masked bool   `json:"masked"`
	Group  string `json:"group"`
}

// envGroup sorts a variable into the part of the system it configures.
func envGroup(k string) string {
	switch {
	case platformKey.MatchString(k):
		return "platform"
	case strings.HasPrefix(k, "SCHWAB_"):
		return "schwab"
	case strings.HasPrefix(k, "ALPACA_") || strings.HasPrefix(k, "MARKET") || strings.HasPrefix(k, "MARKOV"):
		return "market data"
	case strings.HasPrefix(k, "SMTP_") || k == "EMAIL_RECIPIENT":
		return "email"
	case strings.HasPrefix(k, "DASHBOARD_") || strings.HasPrefix(k, "CRON_"):
		return "access"
	}
	return "app"
}

// envEntries returns every variable of environ ("KEY=value"), sorted by group then key, with the
// value of each sensitive one replaced by a mask.
func envEntries(environ []string) []envEntry {
	out := make([]envEntry, 0, len(environ))
	for _, kv := range environ {
		k, v, _ := strings.Cut(kv, "=")
		if k == "" {
			continue
		}
		e := envEntry{Key: k, Value: v, Group: envGroup(k)}
		if sensitiveKey.MatchString(k) {
			e.Value, e.Masked = maskedValue, true
		}
		out = append(out, e)
	}
	order := map[string]int{"app": 0, "market data": 1, "schwab": 2, "email": 3, "access": 4, "platform": 5}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return order[out[i].Group] < order[out[j].Group]
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// visibleEnv drops the platform group unless all is set.
func visibleEnv(entries []envEntry, all bool) []envEntry {
	if all {
		return entries
	}
	var out []envEntry
	for _, e := range entries {
		if e.Group != "platform" {
			out = append(out, e)
		}
	}
	return out
}

type envView struct {
	All     bool       `json:"all"`
	Hidden  int        `json:"hidden_platform_variables"`
	Entries []envEntry `json:"entries"`
}

var envPage = newPage("env", `
{{with .D}}
<div class="card">{{len .Entries}} variable(s) shown{{if not .All}}, {{.Hidden}} platform variable(s) hidden &middot; <a href="/env?all=1">show all</a>{{else}} &middot; <a href="/env">hide platform variables</a>{{end}}
 &middot; <a href="/config">config object</a></div>
{{$g := ""}}<table>
{{range .Entries}}{{if ne .Group $g}}{{$g = .Group}}<tr><th colspan="2">{{.Group}}</th></tr>{{end}}
<tr><td><code>{{.Key}}</code></td><td class="{{if .Masked}}muted{{end}}" style="word-break:break-all">{{.Value}}</td></tr>{{end}}
</table>
{{end}}`)

// registerEnv mounts /env behind the dashboard's Basic Auth, like the other human pages. The
// config object is loaded into the process environment at start-up, so this shows what the app
// is running with, including STRATEGY_ALLOWLIST.
func registerEnv(root *http.ServeMux) error {
	h, err := dashboard.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		all := r.URL.Query().Get("all") == "1"
		entries := envEntries(os.Environ())
		shown := visibleEnv(entries, all)
		writePage(w, r, envPage, "Environment", "Values of passwords, tokens, keys and account numbers are hidden.",
			envView{All: all, Hidden: len(entries) - len(shown), Entries: shown})
	}))
	if err != nil {
		return fmt.Errorf("/env: %w", err)
	}
	root.Handle("/env", h)
	return nil
}
