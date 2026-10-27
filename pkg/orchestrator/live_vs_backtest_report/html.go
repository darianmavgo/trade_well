package live_vs_backtest_report

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"

	orchestratorsql "github.com/darianmavgo/trade_well/sql/orchestrator"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/runtables"
)

// render reads this run's report tables (and the live_vs_backtest_trade and live_vs_backtest_slippage
// tables they sit beside) after they are written and returns the HTML page.
func render(run *runtables.Run, db *sql.DB) (string, error) {
	p, err := loadPage(run, db)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := pageTmpl.Execute(&b, p); err != nil {
		return "", err
	}
	return b.String(), nil
}

type page struct {
	BuiltAt       string
	Allowlist     string
	Account       string
	MarketDB      string
	MarketTable   string
	RunID         string
	HTML          string
	Command       string
	GreenlitCash  template.HTML
	GreenlitClass string
	Status        string
	StatusClass   string
	Notes         []note
	Strategies    []strategyView
	Slippage      []slipView
	Decisions     []decisionView
	Trips         []tripView
}

type slipView struct {
	Strategy, Fills, Fees            string
	Entry, Exit, Worst, Limit, Delay template.HTML
}

type strategyView struct {
	Strategy, Closed, CashClass, Greenlit string
	Cash                                  template.HTML
}

type decisionView struct {
	Account, Strategy, Symbol, AsOf, Live, Model string
	LiveRet, ModelRet                            template.HTML
	EntryBps, ExitBps                            template.HTML
	Signal, Outcome                              string
	LiveReason, ModelReason, Skip                string
}

type tripView struct {
	Account, Strategy, Symbol, Greenlit string
	BuyID, SellID                       string
	BuyPx, SellPx, Qty, Fees            string
	Cash, Ret                           template.HTML
	CashClass                           string
	Opened, Closed, Status              string
}

func loadPage(run *runtables.Run, db *sql.DB) (page, error) {
	ctx := context.Background()
	meta := map[string]string{}
	rows, err := run.Query(ctx, orchestratorsql.Q("live_vs_backtest_report/load_page"))
	if err != nil {
		return page{}, err
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return page{}, err
		}
		meta[k] = v
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return page{}, err
	}
	rows.Close()

	p := page{
		BuiltAt:     meta["built_at"],
		Allowlist:   meta["allowlist"],
		Account:     meta["account"],
		MarketDB:    meta["market_db"],
		MarketTable: meta["market_table"],
		RunID:       meta["run_id"],
		HTML:        meta["html"],
	}
	if p.Allowlist == "" {
		p.Allowlist = "(none)"
	}
	p.Command = commandLine(p)

	notes, err := run.Query(ctx, orchestratorsql.Q("live_vs_backtest_report/load_page_2"))
	if err != nil {
		return page{}, err
	}
	for notes.Next() {
		var n note
		if err := notes.Scan(&n.Severity, &n.Message); err != nil {
			notes.Close()
			return page{}, err
		}
		p.Notes = append(p.Notes, n)
	}
	if err := notes.Err(); err != nil {
		notes.Close()
		return page{}, err
	}
	notes.Close()
	p.Status = "live_vs_backtest_report OK"
	p.StatusClass = "ok"
	if len(p.Notes) > 0 {
		p.Status = "live_vs_backtest_report OK, with warnings"
		p.StatusClass = "warn"
	}

	var green sql.NullFloat64
	if err := run.QueryRow(ctx, orchestratorsql.Q("live_vs_backtest_report/load_page_3")).Scan(&green); err != nil {
		return page{}, err
	}
	p.GreenlitCash = formatMoney(green)
	p.GreenlitClass = moneyClass(green)

	p.Strategies, err = loadStrategies(ctx, run)
	if err != nil {
		return page{}, err
	}
	p.Slippage, err = loadSlippage(ctx, db)
	if err != nil {
		return page{}, err
	}
	p.Decisions, err = loadDecisions(ctx, db, p.Account)
	if err != nil {
		return page{}, err
	}
	p.Trips, err = loadTrips(ctx, run)
	if err != nil {
		return page{}, err
	}
	return p, nil
}

func commandLine(p page) string {
	parts := []string{"orchestrator", "live_vs_backtest_report"}
	if p.Account != "" {
		parts = append(parts, "--account", shellArg(p.Account))
	}
	if p.MarketDB != "" {
		parts = append(parts, "--market-db", shellArg(p.MarketDB))
	}
	if p.MarketTable != "" {
		parts = append(parts, "--table", shellArg(p.MarketTable))
	}
	parts = append(parts, "--html", shellArg(p.HTML))
	return strings.Join(parts, " ")
}

func shellArg(s string) string {
	if s == "" || strings.ContainsAny(s, " \t'\"") {
		return strconv.Quote(s)
	}
	return s
}

func loadStrategies(ctx context.Context, run *runtables.Run) ([]strategyView, error) {
	rows, err := run.Query(ctx, orchestratorsql.Q("live_vs_backtest_report/load_strategies"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []strategyView
	for rows.Next() {
		var id string
		var closed, green int
		var cash sql.NullFloat64
		if err := rows.Scan(&id, &closed, &cash, &green); err != nil {
			return nil, err
		}
		if id == "" {
			id = "(no strategy)"
		}
		label := "no"
		if green == 1 {
			label = "yes"
		}
		out = append(out, strategyView{
			Strategy: id, Closed: strconv.Itoa(closed),
			Cash: formatMoney(cash), CashClass: moneyClass(cash), Greenlit: label,
		})
	}
	return out, rows.Err()
}

func loadSlippage(ctx context.Context, db *sql.DB) ([]slipView, error) {
	rows, err := db.QueryContext(ctx, orchestratorsql.Q("live_vs_backtest_report/load_slippage"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []slipView
	for rows.Next() {
		var s slipView
		var fills int
		var fees float64
		var avgE, avgX, worst, limit, delay sql.NullFloat64
		if err := rows.Scan(&s.Strategy, &fills, &avgE, &avgX, &worst, &limit, &delay, &fees); err != nil {
			return nil, err
		}
		s.Fills = strconv.Itoa(fills)
		s.Entry = formatBps(avgE)
		s.Exit = formatBps(avgX)
		s.Worst = formatBps(worst)
		s.Limit = formatBps(limit)
		s.Delay = formatBps(delay)
		s.Fees = formatDollars(fees)
		out = append(out, s)
	}
	return out, rows.Err()
}

func loadDecisions(ctx context.Context, db *sql.DB, account string) ([]decisionView, error) {
	q := orchestratorsql.Q("live_vs_backtest_report/load_decisions")
	var args []any
	if account != "" {
		q += ` WHERE account_number = ? OR account_number = ''`
		args = append(args, account)
	}
	rows, err := db.QueryContext(ctx, q+` ORDER BY strategy_id, as_of_date, account_number`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []decisionView
	for rows.Next() {
		var d decisionView
		var liveRet, modelRet float64
		var ent, exit sql.NullFloat64
		var sig, outc int
		if err := rows.Scan(&d.Account, &d.Strategy, &d.Symbol, &d.AsOf, &d.Live, &d.Model,
			&liveRet, &modelRet, &ent, &exit, &sig, &outc, &d.LiveReason, &d.ModelReason, &d.Skip); err != nil {
			return nil, err
		}
		d.LiveRet = formatPct(sql.NullFloat64{Float64: liveRet, Valid: true})
		d.ModelRet = formatPct(sql.NullFloat64{Float64: modelRet, Valid: true})
		d.EntryBps = formatBps(ent)
		d.ExitBps = formatBps(exit)
		d.Signal = yesNo(sig)
		d.Outcome = yesNo(outc)
		out = append(out, d)
	}
	return out, rows.Err()
}

func loadTrips(ctx context.Context, run *runtables.Run) ([]tripView, error) {
	rows, err := run.Query(ctx, orchestratorsql.Q("live_vs_backtest_report/load_trips"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []tripView
	for rows.Next() {
		var t tripView
		var green int
		var buy, sell, qty, fees float64
		var cash, ret sql.NullFloat64
		if err := rows.Scan(&t.Account, &t.Strategy, &t.Symbol, &green, &t.BuyID, &t.SellID,
			&buy, &sell, &qty, &fees, &cash, &ret, &t.Opened, &t.Closed, &t.Status); err != nil {
			return nil, err
		}
		if t.Strategy == "" {
			t.Strategy = "(no strategy)"
		}
		t.Greenlit = yesNo(green)
		t.BuyPx = formatPlain(buy)
		t.SellPx = formatPlain(sell)
		t.Qty = formatQty(qty)
		t.Fees = formatDollars(fees)
		t.Cash = formatMoney(cash)
		t.CashClass = moneyClass(cash)
		t.Ret = formatPct(ret)
		out = append(out, t)
	}
	return out, rows.Err()
}

func yesNo(n int) string {
	if n == 1 {
		return "yes"
	}
	return "no"
}

func formatMoney(n sql.NullFloat64) template.HTML {
	if !n.Valid {
		return "—"
	}
	cents := int64(math.Round(n.Float64 * 100))
	sign := ""
	if cents > 0 {
		sign = "+"
	} else if cents < 0 {
		sign = "−"
		cents = -cents
	}
	return template.HTML(fmt.Sprintf("%s$%d.%02d", sign, cents/100, cents%100))
}

func formatDollars(v float64) string {
	cents := int64(math.Round(v * 100))
	if cents < 0 {
		cents = -cents
	}
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}

func moneyClass(n sql.NullFloat64) string {
	if !n.Valid {
		return ""
	}
	cents := int64(math.Round(n.Float64 * 100))
	if cents > 0 {
		return "pos"
	}
	if cents < 0 {
		return "neg"
	}
	return ""
}

func formatPct(n sql.NullFloat64) template.HTML {
	if !n.Valid {
		return "—"
	}
	s := fmt.Sprintf("%+.2f%%", n.Float64*100)
	s = strings.ReplaceAll(s, "-", "−")
	return template.HTML(s)
}

func formatBps(n sql.NullFloat64) template.HTML {
	if !n.Valid {
		return "—"
	}
	s := fmt.Sprintf("%+.1f", n.Float64)
	s = strings.ReplaceAll(s, "-", "−")
	return template.HTML(s)
}

func formatPlain(v float64) string {
	return strconv.FormatFloat(v, 'f', 4, 64)
}

func formatQty(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

var pageTmpl = template.Must(template.New("report").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Live vs backtest</title>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=IBM+Plex+Mono:wght@400;600&family=IBM+Plex+Sans:wght@400;600&display=swap">
<style>
:root{
  --bg:#f6f7f9; --panel:#ffffff; --ink:#1b2430; --muted:#5d6b7c; --line:#dde2e9; --code-bg:#eef1f5;
  --accent:#1f5f8b; --danger:#a3242b; --warn:#a35a00; --ok:#0f7a5c;
}
body{background:var(--bg);color:var(--ink);font:14px/1.55 "IBM Plex Sans",system-ui,-apple-system,"Segoe UI",sans-serif;padding:28px 16px 56px}
.wrap{max-width:980px;margin:0 auto}
h1{font:600 28px/1.15 "IBM Plex Sans",system-ui,sans-serif;margin:0 0 6px}
h2{font:600 18px/1.3 "IBM Plex Sans",system-ui,sans-serif;margin:34px 0 6px}
p{margin:0 0 10px;max-width:74ch}
.lede{color:var(--muted)}
code,pre{font-family:"IBM Plex Mono",ui-monospace,Menlo,Consolas,monospace}
code{font-size:12px;background:var(--code-bg);padding:1px 5px;border-radius:3px;word-break:break-word}
pre{background:var(--code-bg);padding:14px 16px;border-radius:6px;overflow-x:auto;font-size:12px;line-height:1.5;margin:10px 0 16px}
pre code{background:none;padding:0}
.callout{border:1px solid var(--line);border-left:3px solid var(--accent);background:var(--panel);padding:12px 16px;border-radius:4px;margin:12px 0}
.callout.ok{border-left-color:var(--ok)}
.callout.warn{border-left-color:var(--warn)}
.scroll{overflow-x:auto;border:1px solid var(--line);border-radius:6px;background:var(--panel);margin:10px 0 16px}
table{border-collapse:collapse;width:100%;min-width:640px}
th{text-align:left;font:600 11px/1.2 "IBM Plex Mono",ui-monospace,monospace;letter-spacing:.04em;text-transform:uppercase;color:var(--muted);padding:10px 12px;border-bottom:1px solid var(--line)}
td{vertical-align:top;padding:10px 12px;border-bottom:1px solid var(--line)}
tr:last-child td{border-bottom:0}
.num{font-variant-numeric:tabular-nums;white-space:nowrap}
.neg{color:var(--danger)} .pos{color:var(--ok)}
footer{margin-top:34px;color:var(--muted);font-size:13px}
</style>
</head>
<body>
<div class="wrap">
<h1>Live vs backtest</h1>
<div class="callout {{.StatusClass}}"><p><strong>{{.Status}}</strong></p></div>
<p class="lede"><a href="?refresh=1">Refresh</a> · built {{.BuiltAt}}. Allowlist <code>{{.Allowlist}}</code>. Market file <code>{{.MarketDB}}</code>, table <code>{{.MarketTable}}</code>.</p>
<p>Greenlit cash is the sum of Schwab trade net amounts on each closed round trip whose strategy is on that allowlist. A trade net already includes fees. A nested bracket sell is paired with its entry. A separate sell is paired with the oldest unmatched buy of the same account and symbol. Ledger returns stay fractions in the database and are shown here as percents. Entry slippage is positive when the live fill paid more than the one-share replay.</p>

<h2>Greenlit cash</h2>
<p class="{{.GreenlitClass}}"><strong>{{.GreenlitCash}}</strong></p>

<h2>Notes</h2>
{{if .Notes}}{{range .Notes}}<div class="callout {{.Severity}}"><p>{{.Message}}</p></div>{{end}}{{else}}<p>No warnings.</p>{{end}}

<h2>Cash by strategy</h2>
{{if .Strategies}}<div class="scroll"><table>
<thead><tr><th>Strategy</th><th>Greenlit</th><th>Closed trips</th><th>Cash</th></tr></thead>
<tbody>{{range .Strategies}}<tr><td><code>{{.Strategy}}</code></td><td>{{.Greenlit}}</td><td class="num">{{.Closed}}</td><td class="num {{.CashClass}}">{{.Cash}}</td></tr>{{end}}</tbody>
</table></div>{{else}}<p>No round trips.</p>{{end}}

<h2>Round trips</h2>
{{if .Trips}}<div class="scroll"><table>
<thead><tr><th>Account</th><th>Strategy</th><th>Symbol</th><th>Buy</th><th>Sell</th><th>Qty</th><th>Fees</th><th>Cash</th><th>Return</th><th>Status</th></tr></thead>
<tbody>{{range .Trips}}<tr>
<td>{{.Account}}</td><td><code>{{.Strategy}}</code></td><td>{{.Symbol}}</td>
<td class="num">{{.BuyPx}}<br><code>{{.BuyID}}</code></td>
<td class="num">{{.SellPx}}<br><code>{{.SellID}}</code></td>
<td class="num">{{.Qty}}</td><td class="num">{{.Fees}}</td>
<td class="num {{.CashClass}}">{{.Cash}}</td><td class="num">{{.Ret}}</td>
<td>{{.Status}}<br>{{.Opened}}<br>{{.Closed}}</td>
</tr>{{end}}</tbody>
</table></div>{{else}}<p>No round trips.</p>{{end}}

<h2>Slippage</h2>
<p><code>live_vs_backtest_slippage</code> averages the ledger per strategy. Entry slippage is positive when the live fill paid more than the replay. Exit slippage is positive when the live sale was higher than the replay.</p>
{{if .Slippage}}<div class="scroll"><table>
<thead><tr><th>Strategy</th><th>Fills</th><th>Avg entry bps</th><th>Avg exit bps</th><th>Worst entry</th><th>Vs limit</th><th>Fill delay</th><th>Fees</th></tr></thead>
<tbody>{{range .Slippage}}<tr>
<td><code>{{.Strategy}}</code></td><td class="num">{{.Fills}}</td>
<td class="num">{{.Entry}}</td><td class="num">{{.Exit}}</td><td class="num">{{.Worst}}</td>
<td class="num">{{.Limit}}</td><td class="num">{{.Delay}}</td><td class="num">{{.Fees}}</td>
</tr>{{end}}</tbody>
</table></div>{{else}}<p>No slippage summary.</p>{{end}}

<h2>Decisions</h2>
<p>Each row is one <code>live_vs_backtest_trade</code> decision: the live book against a one-share replay.</p>
{{if .Decisions}}<div class="scroll"><table>
<thead><tr><th>Strategy</th><th>Symbol</th><th>Signal date</th><th>Live</th><th>Replay</th><th>Entry bps</th><th>Exit bps</th><th>Signal</th><th>Outcome</th></tr></thead>
<tbody>{{range .Decisions}}<tr>
<td><code>{{.Strategy}}</code><br>{{.Account}}</td>
<td>{{.Symbol}}</td><td>{{.AsOf}}</td>
<td>{{.Live}} {{.LiveRet}}<br>{{.LiveReason}}</td>
<td>{{.Model}} {{.ModelRet}}<br>{{.ModelReason}}</td>
<td class="num">{{.EntryBps}}</td><td class="num">{{.ExitBps}}</td>
<td>{{.Signal}}</td><td>{{.Outcome}}{{if .Skip}}<br>{{.Skip}}{{end}}</td>
</tr>{{end}}</tbody>
</table></div>{{else}}<p>The ledger is empty.</p>{{end}}

<h2>Command</h2>
<pre><code>{{.Command}}</code></pre>
<footer>Report tables <code>__{{.RunID}}</code> in Turso. HTML <code>{{.HTML}}</code>. Built {{.BuiltAt}}.</footer>
</div>
</body>
</html>
`))
