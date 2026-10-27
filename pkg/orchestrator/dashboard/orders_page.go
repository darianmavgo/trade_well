package dashboard

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/orders"
)

// handleOrders serves /orders: every order Schwab lists for the configured accounts, bracket
// children included, with the price each one really executed at next to the limit and stop that
// were sent. An HTML page by default; ?format=json returns the same rows. It reads Schwab for this
// request, writes nothing and places nothing. Filters: ?symbol=AMUU, ?status=FILLED and ?days=N
// (1 to 58, default 7).
func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	days := orders.DefaultDays
	if v := q.Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 58 {
			http.Error(w, "days must be an integer from 1 to 58", http.StatusBadRequest)
			return
		}
		days = n
	}
	now := time.Now()
	live := s.fetchLive(r.Context(), now)
	opt := orders.Options{Symbol: q.Get("symbol"), Status: q.Get("status"), Since: now.AddDate(0, 0, -days)}
	rows := orders.Rows(live.Orders, opt)
	var accounts []string
	for a := range live.Accounts {
		accounts = append(accounts, a)
	}
	sort.Strings(accounts)

	w.Header().Set("Cache-Control", "no-store")
	if q.Get("format") == "json" {
		if rows == nil {
			rows = []orders.Row{}
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			FetchedAt    time.Time    `json:"fetched_at"`
			Days         int          `json:"days"`
			AccountsRead []string     `json:"accounts_read"`
			Errors       []string     `json:"errors"`
			Orders       []orders.Row `json:"orders"`
		}{live.FetchedAt, days, accounts, nonNilStrings(live.Errors), rows}); err != nil {
			log.Printf("[orders] encode: %v", err)
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := ordersTmpl.Execute(w, newOrdersView(rows, live.Errors, accounts, days, opt, now)); err != nil {
		log.Printf("[orders] render: %v", err)
	}
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type orderLine struct {
	Closed, Account, Order, Parent, Symbol, Side, Type, Status, Qty string
	Limit, Stop, Fill, FillQty                                      string
	Class                                                           string // bad for a filled stop, ok for a filled sell above its entry...
	Note                                                            string
}

type ordersView struct {
	Updated, Filter string
	Days            int
	Lines           []orderLine
	Errors          []string
	Accounts        string
}

func newOrdersView(rows []orders.Row, errs, accounts []string, days int, opt orders.Options, now time.Time) ordersView {
	v := ordersView{Updated: closedAt(now.UTC().Format(time.RFC3339Nano)), Days: days, Errors: errs}
	v.Accounts = "none read"
	if len(accounts) > 0 {
		last := make([]string, len(accounts))
		for i, a := range accounts {
			last[i] = "..." + a[max(0, len(a)-4):]
		}
		v.Accounts = strings.Join(last, ", ")
	}
	var f []string
	if opt.Symbol != "" {
		f = append(f, "symbol "+strings.ToUpper(opt.Symbol))
	}
	if opt.Status != "" {
		f = append(f, "status "+strings.ToUpper(opt.Status))
	}
	v.Filter = strings.Join(f, ", ")
	for _, r := range rows {
		l := orderLine{Account: "..." + r.Account[max(0, len(r.Account)-4):], Order: r.OrderID, Parent: r.ParentID, Symbol: r.Symbol,
			Side: r.Side, Type: r.Type, Status: r.Status, Qty: fmt.Sprintf("%g", r.Quantity), Limit: "–", Stop: "–", Fill: "–"}
		l.Closed = "–"
		if !r.Closed.IsZero() {
			l.Closed = closedAt(r.Closed.UTC().Format(time.RFC3339Nano))
		}
		if r.Limit != nil && *r.Limit > 0 {
			l.Limit = fmt.Sprintf("$%.2f", *r.Limit)
		}
		if r.Stop != nil && *r.Stop > 0 {
			l.Stop = fmt.Sprintf("$%.2f", *r.Stop)
		}
		if r.Fill != nil {
			l.Fill = fmt.Sprintf("$%.2f", *r.Fill)
			l.FillQty = fmt.Sprintf("%g shares", r.FillQty)
		}
		switch {
		case r.Status == "FILLED" && strings.Contains(r.Type, "STOP"):
			l.Class, l.Note = "bad", "STOP FILLED"
		case r.Status == "FILLED":
			l.Class = "ok"
		case r.Status == "REJECTED" || r.Status == "CANCELED" || r.Status == "EXPIRED":
			l.Class = "muted"
		case r.Status == "WORKING" || r.Status == "QUEUED" || r.Status == "PENDING_ACTIVATION" || r.Status == "ACCEPTED":
			l.Class = "warn"
		}
		v.Lines = append(v.Lines, l)
	}
	return v
}

var ordersTmpl = template.Must(template.New("orders").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>orders</title>
<style>` + pageStyle + `
  td.fill b { font-size:15px; } td small { color:var(--muted); display:block; }
</style></head><body><main>
<h1>Orders {{if .Filter}}<span class="muted">({{.Filter}})</span>{{end}}</h1>
<p class="sub">What Schwab lists for the last {{.Days}} days ({{.Updated}}), read just now. Accounts read: {{.Accounts}}. <b>Limit</b> and <b>Stop</b> are what was sent; <b>Fill</b> is the price it really executed at. They differ: a stop fills below its trigger when the price gaps. Brackets are unfolded, a child showing its parent order. Filters: <code>?symbol=AMUU</code> <code>?status=FILLED</code> <code>?days=30</code> &middot; <a href="?format=json">JSON</a> &middot; <a href="/live_vs_backtest_winrate">win rate</a> &middot; <a href="/strategy">strategies</a>. Read-only.</p>
{{range .Errors}}<p class="sub"><span class="tag bad">live read</span> {{.}}</p>{{end}}
{{if .Lines}}<div class="table-wrap"><table>
  <tr><th>Closed (ET)</th><th>Account</th><th>Order</th><th>Symbol</th><th>Side</th><th>Type</th><th>Status</th><th>Qty</th><th>Limit</th><th>Stop</th><th>Fill</th></tr>
  {{range .Lines}}<tr>
    <td>{{.Closed}}</td><td>{{.Account}}</td><td>{{.Order}}{{if .Parent}}<small>under {{.Parent}}</small>{{end}}</td><td>{{.Symbol}}</td><td>{{.Side}}</td><td>{{.Type}}</td>
    <td><span class="tag {{.Class}}">{{if .Note}}{{.Note}}{{else}}{{.Status}}{{end}}</span></td><td>{{.Qty}}</td><td>{{.Limit}}</td><td>{{.Stop}}</td>
    <td class="fill {{.Class}}">{{if .FillQty}}<b>{{.Fill}}</b><small>{{.FillQty}}</small>{{else}}{{.Fill}}{{end}}</td>
  </tr>{{end}}
</table></div>{{else}}<div class="card muted">No orders match{{if not .Errors}} in the last {{.Days}} days. <a href="?days=58">Last 58 days</a>{{else}}, and Schwab could not be read (see above){{end}}.</div>{{end}}
</main></body></html>`))
