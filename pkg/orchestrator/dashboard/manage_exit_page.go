package dashboard

import (
	"database/sql"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
)

// handleManageExit serves /manage_exit: the manage_exit table, every column, split into
// the open positions, the entries still waiting to fill, and what has closed or been
// cancelled. Same Basic Auth as the rest of the dashboard (it is mounted in Handler's mux).
// Read-only: it never calls Schwab and never writes.
func (s *Server) handleManageExit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDB(); err != nil {
		log.Printf("[manage_exit] page: database unavailable: %v", err)
		http.Error(w, "ERROR: database unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}
	view, err := loadManageExitView(s.db, time.Now())
	if err != nil {
		log.Printf("[manage_exit] page: %v", err)
		http.Error(w, "ERROR: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := manageExitTmpl.Execute(w, view); err != nil {
		log.Printf("[manage_exit] page render: %v", err)
	}
}

// manageExitMaxRows caps how many rows are read; the newest come first.
const manageExitMaxRows = 1000

type exitSection struct {
	Title, Blurb string
	Columns      []string
	Rows         [][]string
	StatusCol    int // index of the status column, -1 when absent
}

type manageExitView struct {
	Updated  string
	Total    int
	Sections []exitSection
}

// columnOrder puts the columns an operator reads first, in reading order; any other
// column of the table follows in table order, so a column added later still shows.
var columnOrder = []string{
	"id", "account_number", "symbol", "strategy_name", "status", "exit_date", "exit_date_source",
	"quantity", "entry_price", "target_profit_price", "stop_loss_price", "entry_time", "entry_filled_at",
	"max_trading_days", "trading_days_held",
	"entry_order_id", "exit_order_id", "exit_reason", "exit_reason_code", "exit_time", "exit_price", "pnl", "pnl_pct",
	"strategy_id", "as_of_date", "entry_fill_price", "exit_fill_price", "filled_qty", "fees",
}

func loadManageExitView(db *sql.DB, now time.Time) (*manageExitView, error) {
	if err := manage_exits.EnsureSchema(db); err != nil {
		return nil, fmt.Errorf("ensure manage_exit schema: %w", err)
	}
	rows, err := db.Query(fmt.Sprintf(`SELECT * FROM manage_exit ORDER BY id DESC LIMIT %d`, manageExitMaxRows))
	if err != nil {
		return nil, fmt.Errorf("read manage_exit: %w", err)
	}
	defer rows.Close()
	tableCols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	// Display order: preferred columns that exist, then the rest.
	var order []int
	used := map[string]bool{}
	for _, want := range columnOrder {
		if i := slices.Index(tableCols, want); i >= 0 {
			order = append(order, i)
			used[want] = true
		}
	}
	for i, c := range tableCols {
		if !used[c] {
			order = append(order, i)
		}
	}
	cols := make([]string, len(order))
	statusCol := -1
	for k, i := range order {
		cols[k] = tableCols[i]
		if tableCols[i] == "status" {
			statusCol = k
		}
	}

	open := exitSection{Title: "Open positions", Blurb: "status OPEN: held, and managed to their exit date.", Columns: cols, StatusCol: statusCol}
	pending := exitSection{Title: "Entries waiting to fill", Blurb: "status PENDING_FILL: accepted by the broker, fill not yet confirmed. The exit date is provisional until the real fill date is known.", Columns: cols, StatusCol: statusCol}
	closed := exitSection{Title: "Closed and cancelled", Blurb: "Every other status, newest first.", Columns: cols, StatusCol: statusCol}
	total := 0
	for rows.Next() {
		vals := make([]any, len(tableCols))
		ptrs := make([]any, len(tableCols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]string, len(order))
		for k, i := range order {
			row[k] = cell(vals[i])
		}
		total++
		switch {
		case statusCol >= 0 && row[statusCol] == manage_exits.TradeStatusOpen:
			open.Rows = append(open.Rows, row)
		case statusCol >= 0 && row[statusCol] == manage_exits.TradeStatusPendingFill:
			pending.Rows = append(pending.Rows, row)
		default:
			closed.Rows = append(closed.Rows, row)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &manageExitView{
		Updated:  now.In(job_runs.EasternTime()).Format("Mon 2006-01-02 15:04:05 MST"),
		Total:    total,
		Sections: []exitSection{open, pending, closed},
	}, nil
}

// cell renders one database value: NULL is empty, times are UTC RFC 3339, floats are
// compact, bytes are text.
func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case []byte:
		return string(x)
	case float64:
		return fmt.Sprintf("%g", x)
	default:
		return strings.TrimSpace(fmt.Sprint(x))
	}
}

var manageExitTmpl = template.Must(template.New("manage_exit").Funcs(template.FuncMap{
	"css": func() template.CSS { return template.CSS(pageStyle) },
	"class": func(status string) string {
		switch status {
		case "OPEN", "CLOSED_PROFIT":
			return "ok"
		case "PENDING_FILL", "CLOSED_TIME_EXPIRATION", "CLOSED_MANUAL":
			return "warn"
		case "CLOSED_STOP_LOSS", "CANCELLED":
			return "bad"
		}
		return ""
	},
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>manage_exit</title>
<style>{{css}}</style>
</head>
<body>
<main>
  <h1>manage_exit</h1>
  <p class="sub">Updated {{.Updated}}. {{.Total}} row(s). <a href="/manage_exit">Reload</a> &middot; <a href="/check_health">check_health</a> &middot; <a href="/">Dashboard</a>. Read-only: written by <code>submit_orders</code> and <code>manage_exits</code>. <code>exit_date</code> is the entry fill date plus the strategy's hold window.</p>
  {{range $s := .Sections}}
  <h2>{{$s.Title}} <span class="muted">({{len $s.Rows}})</span></h2>
  <p class="sub">{{$s.Blurb}}</p>
  {{if $s.Rows}}
  <div class="table-wrap">
  <table>
    <tr>{{range $s.Columns}}<th>{{.}}</th>{{end}}</tr>
    {{range $r := $s.Rows}}<tr>{{range $i, $c := $r}}{{if eq $i $s.StatusCol}}<td><span class="tag {{class $c}}">{{$c}}</span></td>{{else}}<td>{{$c}}</td>{{end}}{{end}}</tr>
    {{end}}
  </table>
  </div>
  {{else}}<p class="muted">None.</p>{{end}}
  {{end}}
</main>
</body>
</html>
`))
