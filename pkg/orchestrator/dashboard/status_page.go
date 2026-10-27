package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategy_status"
)

// handleStatus serves /status/{strategy-id}: the strategy's signal check for today, the
// positions it created (with hold windows, checked against Schwab read live), its staged
// orders and submit attempts, and a list of anything that went wrong. An HTML page by
// default; ?format=json returns the same data as JSON. Read-only, behind the dashboard's
// Basic Auth, and it never writes. Optional ?days=N widens the look-back window (default 7).
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/status"), "/")
	if id == "" || strings.Contains(id, "/") {
		http.Error(w, "usage: /status/{strategy-id}", http.StatusBadRequest)
		return
	}
	days := 0
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 365 {
			http.Error(w, "days must be an integer from 1 to 365", http.StatusBadRequest)
			return
		}
		days = n
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDB(); err != nil {
		log.Printf("[status] database unavailable: %v", err)
		http.Error(w, "ERROR: database unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Broker facts are read live from Schwab for this request; if Schwab cannot be reached the
	// status falls back to the last account_state sync and says so (broker_source, live_errors).
	now := time.Now()
	live := s.fetchLive(r.Context(), now)
	st, err := strategy_status.Build(s.db, id, os.Getenv("STRATEGY_ALLOWLIST"), now, days, live)
	if err != nil {
		log.Printf("[status] %s: %v", id, err)
		http.Error(w, "ERROR: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(st); err != nil {
			log.Printf("[status] %s: encode: %v", id, err)
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := statusTmpl.Execute(w, newStatusView(st, now)); err != nil {
		log.Printf("[status] %s: render: %v", id, err)
	}
}

type statusView struct {
	*strategy_status.Status
	Updated  string
	Sections []exitSection
}

func f2(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.2f", *v)
}

func yn(v *bool) string {
	switch {
	case v == nil:
		return "unknown"
	case *v:
		return "yes"
	}
	return "NO"
}

func newStatusView(st *strategy_status.Status, now time.Time) *statusView {
	v := &statusView{Status: st, Updated: now.In(job_runs.EasternTime()).Format("Mon 2006-01-02 15:04:05 MST")}
	sec := func(title, blurb string, cols []string, statusCol int) *exitSection {
		return &exitSection{Title: title, Blurb: blurb, Columns: cols, StatusCol: statusCol}
	}
	add := func(s *exitSection) { v.Sections = append(v.Sections, *s) }

	probs := sec("Problems", "Anything that went wrong or needs a look, worst first.", []string{"severity", "where", "detail"}, 0)
	for _, sev := range []string{strategy_status.SevProblem, strategy_status.SevWarning, strategy_status.SevInfo} {
		for _, p := range st.Problems {
			if p.Severity == sev {
				probs.Rows = append(probs.Rows, []string{p.Severity, p.Where, p.Detail})
			}
		}
	}
	add(probs)

	sig := sec("Today's signal check", fmt.Sprintf("Result for %s: %s.%s", st.SignalCheck.Date, st.SignalCheck.Result, lastScanLine(st.SignalCheck)),
		[]string{"id", "symbol", "as_of_date", "next_session", "status", "for_today", "buy_limit", "take_profit", "stop_loss", "hold_days", "staged_order_id", "notes"}, 4)
	for _, g := range st.SignalCheck.Signals {
		sig.Rows = append(sig.Rows, []string{fmt.Sprint(g.ID), g.Symbol, g.AsOfDate, g.NextSession, g.Status, fmt.Sprint(g.ForToday),
			fmt.Sprintf("%.2f", g.BuyLimit), fmt.Sprintf("%.2f", g.TakeProfit), fmt.Sprintf("%.2f", g.StopLoss), fmt.Sprint(g.HoldDays), fmt.Sprint(g.StagedOrderID), g.Notes})
	}
	add(sig)

	posCols := []string{"trade", "account", "symbol", "status", "qty", "broker_held", "at_broker", "entry_order", "broker_entry", "entry_filled_at",
		"held_days", "max_days", "exit_date", "source", "days_left", "overdue", "resting_tp", "resting_stop", "filled_exit"}
	posRow := func(p strategy_status.Position) []string {
		held := ""
		if p.BrokerHeldQty != nil {
			held = fmt.Sprintf("%g", *p.BrokerHeldQty)
		}
		return []string{fmt.Sprint(p.TradeID), p.Account, p.Symbol, p.Status, fmt.Sprintf("%g", p.Quantity), held, yn(p.ConfirmedAtBroker), p.EntryOrderID,
			p.BrokerEntryStatus, p.EntryFilledAt, fmt.Sprint(p.TradingDaysHeld), fmt.Sprint(p.MaxTradingDays), p.ExitDate, p.ExitDateSource,
			fmt.Sprint(p.DaysRemaining), fmt.Sprint(p.ExitOverdue), f2(p.RestingTakeProfit), f2(p.RestingStop), p.FilledExitOrderID}
	}
	pos := sec("Positions", "This strategy's open and pending trades, checked against "+brokerSourceText(st)+".", posCols, 3)
	for _, p := range st.Positions {
		pos.Rows = append(pos.Rows, posRow(p))
	}
	add(pos)

	closed := sec("Recently closed", fmt.Sprintf("Closed in the last %d days.", st.WindowDays), append(append([]string{}, posCols[:12]...), "exit_time", "exit_reason"), 3)
	for _, p := range st.Closed {
		closed.Rows = append(closed.Rows, append(append([]string{}, posRow(p)[:12]...), p.ExitTime, p.ExitReason))
	}
	add(closed)

	work := sec("Working at the broker", "Orders live at Schwab on this strategy's symbols.", []string{"account", "symbol", "order_id", "instruction", "order_type", "status", "price", "stop_price", "qty"}, 5)
	for _, w := range st.BrokerWorking {
		work.Rows = append(work.Rows, []string{w.Account, w.Symbol, w.OrderID, w.Instruction, w.OrderType, w.Status, fmt.Sprintf("%.2f", w.Price), fmt.Sprintf("%.2f", w.StopPrice), fmt.Sprintf("%g", w.Quantity)})
	}
	add(work)

	if len(st.UntrackedShares) > 0 {
		un := sec("Untracked shares", "Held at the broker beyond what this strategy's OPEN rows account for.", []string{"account", "symbol", "broker_held", "tracked_open"}, -1)
		for _, u := range st.UntrackedShares {
			un.Rows = append(un.Rows, []string{u.Account, u.Symbol, fmt.Sprintf("%g", u.Held), fmt.Sprintf("%g", u.Tracked)})
		}
		add(un)
	}

	stg := sec("Staged orders", fmt.Sprintf("From the last %d days.", st.WindowDays), []string{"id", "account", "symbol", "status", "qty", "entry_price", "order_id", "broker_status", "reject_reason", "staged_at"}, 3)
	for _, o := range st.Staged {
		stg.Rows = append(stg.Rows, []string{fmt.Sprint(o.ID), o.Account, o.Symbol, o.Status, fmt.Sprintf("%g", o.Quantity), fmt.Sprintf("%.2f", o.EntryPrice), o.OrderID, o.BrokerStatus, o.RejectReason, o.StagedAt})
	}
	add(stg)

	att := sec("Submit attempts", "One row per try at placing a staged order.", []string{"staged_order", "status", "broker_order", "at", "detail"}, 1)
	for _, a := range st.Attempts {
		att.Rows = append(att.Rows, []string{fmt.Sprint(a.StagedOrderID), a.Status, a.BrokerOrderID, a.At, a.Detail})
	}
	add(att)
	return v
}

func lastScanLine(sc strategy_status.SignalCheck) string {
	if sc.LastScan == nil {
		return " No scan_market run on record."
	}
	return fmt.Sprintf(" Last scan: %s at %s (%s).", sc.LastScan.Status, sc.LastScan.FinishedAt, sc.LastScan.Summary)
}

func brokerSourceText(st *strategy_status.Status) string {
	switch st.BrokerSource {
	case "live":
		return "Schwab, read live for this page"
	case "mirror":
		return "the last account_state sync (" + st.BrokerSnapshotAt + "), not live"
	}
	return "no broker data"
}

var statusTmpl = template.Must(template.New("status").Funcs(template.FuncMap{
	"css":   func() template.CSS { return template.CSS(pageStyle) },
	"class": statusClass,
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>status {{.Strategy}}</title>
<style>{{css}}</style>
</head>
<body>
<main>
  <h1>{{.Strategy}} <span class="tag {{class .Verdict}}">{{.Verdict}}</span></h1>
  <p class="sub">Updated {{.Updated}}. {{if .Greenlit}}In STRATEGY_ALLOWLIST.{{else}}<b>Not in STRATEGY_ALLOWLIST.</b>{{end}} Broker data: {{.BrokerSource}}{{if .BrokerSnapshotAt}} (sync {{.BrokerSnapshotAt}}){{end}}. Look-back {{.WindowDays}} days (<a href="?days=30">30</a>). <a href="">Reload</a> &middot; <a href="?format=json">JSON</a> &middot; <a href="/strategy">All strategies</a> &middot; <a href="/manage_exit">manage_exit</a> &middot; <a href="/check_health">check_health</a> &middot; <a href="/">Dashboard</a>. Read-only.</p>
  {{range .LiveErrors}}<p class="sub"><span class="tag bad">live read</span> {{.}}</p>{{end}}
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

// fetchLive reads positions and orders for the configured accounts from Schwab. It never
// fails the request: problems come back in Live.Errors.
func (s *Server) fetchLive(ctx context.Context, now time.Time) *strategy_status.Live {
	return strategy_status.FetchLiveFromEnv(ctx, now, newTraderClient, schwabClientInitTimeout, 60*time.Second)
}

// handleStrategies serves /strategy: the status of every greenlit strategy (STRATEGY_ALLOWLIST)
// on one page, one summary row each linking to /status/{id}. HTML by default; ?format=json
// returns the full per-strategy statuses. Schwab is read once for all of them.
func (s *Server) handleStrategies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if strings.Trim(strings.TrimPrefix(r.URL.Path, "/strategy"), "/") != "" {
		http.NotFound(w, r)
		return
	}
	days := 0
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 365 {
			http.Error(w, "days must be an integer from 1 to 365", http.StatusBadRequest)
			return
		}
		days = n
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDB(); err != nil {
		log.Printf("[strategy] database unavailable: %v", err)
		http.Error(w, "ERROR: database unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}
	allowlist := os.Getenv("STRATEGY_ALLOWLIST")
	now := time.Now()
	live := s.fetchLive(r.Context(), now)
	all, err := strategy_status.BuildAll(s.db, allowlist, now, days, live)
	if err != nil {
		log.Printf("[strategy] %v", err)
		http.Error(w, "ERROR: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(all); err != nil {
			log.Printf("[strategy] encode: %v", err)
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := strategiesTmpl.Execute(w, newStrategiesView(all, live, now)); err != nil {
		log.Printf("[strategy] render: %v", err)
	}
}

type strategiesView struct {
	Updated    string
	Count      int
	Allowlist  string
	LiveErrors []string
	BrokerNote string
	Sections   []exitSection
	Strategies []strategyProblems
}

// strategyProblems is one strategy's problems, for the detail list under the summary.
type strategyProblems struct {
	ID       string
	Verdict  string
	Problems []strategy_status.Problem
}

func newStrategiesView(all []*strategy_status.Status, live *strategy_status.Live, now time.Time) *strategiesView {
	v := &strategiesView{
		Updated: now.In(job_runs.EasternTime()).Format("Mon 2006-01-02 15:04:05 MST"), Count: len(all),
		Allowlist: os.Getenv("STRATEGY_ALLOWLIST"),
	}
	if live != nil {
		v.LiveErrors = live.Errors
	}
	sum := exitSection{
		Title: "Greenlit strategies", StatusCol: 1,
		Blurb:   "One row per strategy in STRATEGY_ALLOWLIST (stack members listed separately). Click a strategy for its full status.",
		Columns: []string{"strategy", "verdict", "signal_today", "why", "last_scan", "open", "pending_fill", "next_exit_date", "problems", "warnings", "staged_7d", "broker"},
	}
	for _, st := range all {
		open, pending, problems, warnings := 0, 0, 0, 0
		next := ""
		for _, p := range st.Positions {
			switch p.Status {
			case "OPEN":
				open++
				if p.ExitDate != "" && (next == "" || p.ExitDate < next) {
					next = p.ExitDate
				}
			case "PENDING_FILL":
				pending++
			}
		}
		for _, p := range st.Problems {
			switch p.Severity {
			case strategy_status.SevProblem:
				problems++
			case strategy_status.SevWarning:
				warnings++
			}
		}
		scan := ""
		if st.SignalCheck.LastScan != nil {
			scan = st.SignalCheck.LastScan.Status + " " + st.SignalCheck.LastScan.FinishedAt
		}
		sum.Rows = append(sum.Rows, []string{st.Strategy, st.Verdict, st.SignalCheck.Result, st.SignalCheck.Why, scan, fmt.Sprint(open), fmt.Sprint(pending), next,
			fmt.Sprint(problems), fmt.Sprint(warnings), fmt.Sprint(len(st.Staged)), st.BrokerSource})
		if len(st.Problems) > 0 {
			v.Strategies = append(v.Strategies, strategyProblems{ID: st.Strategy, Verdict: st.Verdict, Problems: st.Problems})
		}
		if v.BrokerNote == "" {
			v.BrokerNote = brokerSourceText(st)
		}
	}
	v.Sections = []exitSection{sum}
	return v
}

var strategiesTmpl = template.Must(template.New("strategy").Funcs(template.FuncMap{
	"css":   func() template.CSS { return template.CSS(pageStyle) },
	"class": statusClass,
}).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>strategy status</title>
<style>{{css}}</style>
</head>
<body>
<main>
  <h1>Strategies <span class="muted">({{.Count}} greenlit)</span></h1>
  <p class="sub">Updated {{.Updated}}. Broker data: {{.BrokerNote}}. <a href="">Reload</a> &middot; <a href="?format=json">JSON</a> &middot; <a href="/live_vs_backtest_winrate">win rate vs backtest</a> &middot; <a href="/manage_exit">manage_exit</a> &middot; <a href="/check_health">check_health</a> &middot; <a href="/">Dashboard</a>. Read-only. STRATEGY_ALLOWLIST: <code>{{.Allowlist}}</code></p>
  {{range .LiveErrors}}<p class="sub"><span class="tag bad">live read</span> {{.}}</p>{{end}}
  {{range $s := .Sections}}
  {{if $s.Rows}}
  <div class="table-wrap">
  <table>
    <tr>{{range $s.Columns}}<th>{{.}}</th>{{end}}</tr>
    {{range $r := $s.Rows}}<tr>{{range $i, $c := $r}}{{if eq $i 0}}<td><a href="/status/{{$c}}">{{$c}}</a></td>{{else if eq $i $s.StatusCol}}<td><span class="tag {{class $c}}">{{$c}}</span></td>{{else}}<td>{{$c}}</td>{{end}}{{end}}</tr>
    {{end}}
  </table>
  </div>
  {{else}}<p class="muted">STRATEGY_ALLOWLIST is empty: no strategy is greenlit.</p>{{end}}
  {{end}}
  {{range $st := .Strategies}}
  <h2><a href="/status/{{$st.ID}}">{{$st.ID}}</a> <span class="tag {{class $st.Verdict}}">{{$st.Verdict}}</span></h2>
  <div class="table-wrap">
  <table>
    <tr><th>severity</th><th>where</th><th>detail</th></tr>
    {{range $st.Problems}}<tr><td><span class="tag {{class .Severity}}">{{.Severity}}</span></td><td>{{.Where}}</td><td>{{.Detail}}</td></tr>
    {{end}}
  </table>
  </div>
  {{end}}
</main>
</body>
</html>
`))

// statusClass maps a status word to a tag colour: ok, warn or bad ("" for anything else).
func statusClass(v string) string {
	switch strings.ToUpper(v) {
	case "OK", "OPEN", "CLOSED_PROFIT", "FILLED", "SIGNAL", "YES", "SUBMITTED", "WORKING", "TRUE", "LIVE", "NO_SIGNAL":
		return "ok"
	case "ATTENTION", "WARNING", "PENDING_FILL", "PENDING", "INFO", "CLOSED_TIME_EXPIRATION", "CLOSED_MANUAL", "NEW", "QUEUED", "DISMISSED", "UNKNOWN", "NOT_RUN", "MIRROR":
		return "warn"
	case "PROBLEM", "CLOSED_STOP_LOSS", "CANCELLED", "CANCELED", "REJECTED", "FAILED_SUBMIT", "NO", "SCAN_FAILED", "EXPIRED":
		return "bad"
	}
	return ""
}
