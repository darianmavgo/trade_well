package dashboard

import (
	"cmp"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/live_vs_backtest_winrate"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategy_status"
)

// handleLiveVsBacktestWinrate serves /live_vs_backtest_winrate: every strategy's backtest in-sample, backtest out-of-sample
// and live win rate on one scale, with the live 95% interval and a verdict. An HTML page by
// default; ?format=json returns the same rows. Read-only: it never writes and never calls
// Schwab. The backtest side is whatever `orchestrator live_vs_backtest_winrate` last copied in.
// pageReuseFor is how long a view of this page reads the tables an earlier view built instead of
// building new ones in Turso.
const pageReuseFor = time.Minute

func (s *Server) handleLiveVsBacktestWinrate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureDB(); err != nil {
		log.Printf("[live_vs_backtest_winrate] database unavailable: %v", err)
		http.Error(w, "ERROR: database unavailable: "+err.Error(), http.StatusInternalServerError)
		return
	}
	opt := live_vs_backtest_winrate.Options{RefDB: os.Getenv("LIVE_VS_BACKTEST_REFDB"), MaxAge: pageReuseFor}
	if opt.RefDB == "" {
		if folder := os.Getenv("APP_FOLDER"); folder != "" {
			opt.RefDB = filepath.Join(folder, "refdata", "strategies.db")
		}
	}
	opt.IDs = strategy_status.Members(os.Getenv("STRATEGY_ALLOWLIST"))
	days := 3
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 60 {
			http.Error(w, "days must be an integer from 1 to 60", http.StatusBadRequest)
			return
		}
		days = n
	}
	rows, err := live_vs_backtest_winrate.Compare(r.Context(), s.db, opt)
	if err != nil {
		log.Printf("[live_vs_backtest_winrate] %v", err)
		http.Error(w, "ERROR: "+err.Error(), http.StatusInternalServerError)
		return
	}
	recent, err := live_vs_backtest_winrate.Recent(r.Context(), s.db, days, pageReuseFor)
	if err != nil {
		log.Printf("[live_vs_backtest_winrate] recent: %v", err)
		http.Error(w, "ERROR: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(struct {
			Strategies   []live_vs_backtest_winrate.Row   `json:"strategies"`
			RecentDays   int                              `json:"recent_days"`
			RecentTrades []live_vs_backtest_winrate.Trade `json:"recent_trades"`
		}{rows, days, nonNilTrades(recent)}); err != nil {
			log.Printf("[live_vs_backtest_winrate] encode: %v", err)
		}
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := winrateTmpl.Execute(w, newLiveVsBacktestWinrateView(rows, recent, days)); err != nil {
		log.Printf("[live_vs_backtest_winrate] render: %v", err)
	}
}

// winrateCell is one number in the summary table: the rate large, its trade count small, and a
// bar on the shared 0-100% scale.
type winrateCell struct {
	Has   bool
	Rate  string // "85%"
	Trade string // "33 trades"
	Pct   string // bar width
	Lo    string // live interval whisker, percent of scale
	Hi    string
}

type winrateLine struct {
	live_vs_backtest_winrate.Row
	IS, OOS, All, Live, Model winrateCell
	Class                     string // ok, warn, bad or muted
	Headline                  string
	Chart                     *winrateChart
}

// recentLine is one closed trade as the table shows it.
type recentLine struct {
	Closed, Strategy, Account, Symbol, Reason string
	Qty, Entry, Exit, Return, PnL             string
	Class                                     string // bad for a stop loss, ok for a gain, muted otherwise
	Stop                                      bool
	Sub                                       string // what it is: STOP LOSS, TARGET, ...
	EntryNote, ExitNote                       string // "fill", or what the price is when no fill is recorded
}

type winrateView struct {
	Days       int
	Recent     []recentLine
	Stops      int           // stop losses among Recent
	Unmeasured int           // Recent rows still measured from the limit and trigger, not the fills
	Greenlit   []winrateLine // strategies on the allowlist, first
	Other      []winrateLine // anything else with data
	Counts     []countChip
	None       bool
}

type countChip struct {
	Verdict string
	N       int
	Class   string
}

func newLiveVsBacktestWinrateView(rows []live_vs_backtest_winrate.Row, recent []live_vs_backtest_winrate.Trade, days int) winrateView {
	v := winrateView{None: len(rows) == 0 && len(recent) == 0, Days: days}
	v.Recent, v.Stops, v.Unmeasured = recentLines(recent)
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.Verdict]++
		l := winrateLine{Row: r, Class: verdictClass(r.Verdict), Headline: headline(r)}
		l.IS = rateCell(r.ISWinRate, r.ISTrades)
		l.OOS = rateCell(r.OOSWinRate, r.OOSTrades)
		l.All = rateCell(r.AllWinRate, r.AllTrades)
		l.Live = rateCell(r.LiveWinRate, r.LiveDecisions)
		l.Model = rateCell(r.ModelWinRate, r.ModelTrades)
		if r.LiveCILow != nil && r.LiveCIHigh != nil && l.Live.Has {
			l.Live.Lo, l.Live.Hi = pct(*r.LiveCILow), pct(*r.LiveCIHigh)
		}
		l.Chart = newLiveVsBacktestWinrateChart(r)
		if r.Greenlit {
			v.Greenlit = append(v.Greenlit, l)
		} else {
			v.Other = append(v.Other, l)
		}
	}
	for _, name := range []string{live_vs_backtest_winrate.Below, live_vs_backtest_winrate.InLine, live_vs_backtest_winrate.Above, live_vs_backtest_winrate.TooEarly, live_vs_backtest_winrate.NoBacktest} {
		if counts[name] > 0 {
			v.Counts = append(v.Counts, countChip{Verdict: name, N: counts[name], Class: verdictClass(name)})
		}
	}
	return v
}

func rateCell(rate *float64, n int) winrateCell {
	if rate == nil {
		return winrateCell{}
	}
	t := fmt.Sprintf("%d trades", n)
	if n == 1 {
		t = "1 trade"
	}
	return winrateCell{Has: true, Rate: fmt.Sprintf("%.0f%%", *rate*100), Trade: t, Pct: pct(*rate)}
}

func pct(f float64) string {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return fmt.Sprintf("%.1f", f*100)
}

func verdictClass(v string) string {
	switch v {
	case live_vs_backtest_winrate.Below:
		return "bad"
	case live_vs_backtest_winrate.InLine, live_vs_backtest_winrate.Above:
		return "ok"
	case live_vs_backtest_winrate.NoBacktest:
		return "warn"
	}
	return "muted"
}

// headline says in one sentence what the verdict means for this strategy.
func headline(r live_vs_backtest_winrate.Row) string {
	held := ""
	if r.OpenPositions > 0 || r.PendingFills > 0 {
		held = fmt.Sprintf(" Now holding %d open, %d waiting to fill.", r.OpenPositions, r.PendingFills)
	}
	switch r.Verdict {
	case live_vs_backtest_winrate.NoBacktest:
		return "No backtest win rate for this strategy yet. Run orchestrator live_vs_backtest_sync -reports-dir <backtestgosqlite/data/reports>, then orchestrator live_vs_backtest_winrate -export-baseline pkg/live_vs_backtest_winrate/baseline.json and rebuild." + held
	case live_vs_backtest_winrate.TooEarly:
		return fmt.Sprintf("%d of %d closed live decisions needed before this can be judged.", r.LiveDecisions, live_vs_backtest_winrate.MinLive) + held
	}
	kind := "out-of-sample"
	switch r.BaseKind {
	case "is":
		kind = "in-sample"
	case "all":
		kind = "whole-window backtest (it includes the data the strategy was picked on, so it flatters)"
	}
	d := *r.LiveMinusBase * 100
	switch r.Verdict {
	case live_vs_backtest_winrate.Below:
		s := fmt.Sprintf("Live is %.0f points under the %s rate, and the whole 95%% range is under it.", -d, kind)
		if r.LiveMinusMod != nil && *r.LiveMinusMod < -0.1 {
			s += " The backtest replaying these same trades is better than live, so look at fills."
		} else if r.ModelWinRate != nil {
			s += " The backtest replaying these same trades is also low, so the edge itself is weaker than it tested."
		}
		return s + held
	case live_vs_backtest_winrate.Above:
		return fmt.Sprintf("Live is %.0f points over the %s rate, and the whole 95%% range is over it.", d, kind) + held
	}
	return fmt.Sprintf("Live is %+.0f points from the %s rate, within what %d trades can show.", d, kind, r.LiveDecisions) + held
}

// winrateChart is the live win rate after each closed trade, drawn as an SVG that fills in as
// trades close: x is the trade number, y the win rate, the shaded band its 95% range, and
// the dashed lines the in-sample and out-of-sample rates to stay near.
type winrateChart struct {
	W, H   int
	Grid   []chartGrid
	Ticks  []chartTick
	Lines  []chartLine
	Curves []chartCurve // backtest in-sample and out-of-sample running win rates
	Line   string       // live win rate polyline points
	Band   string       // 95% range polygon points
	Dots   []chartDot
	Empty  bool
	Plot   chartBox
}

type ref struct {
	rate  *float64
	label string
	class string
}

type chartCurve struct{ Class, Points, Tip string }

type chartBox struct{ X, Y, W, H float64 }
type chartGrid struct{ Y, Label string }
type chartTick struct{ X, Label string }
type chartLine struct {
	Y, X1, X2, Label, LabelY, Class string
}
type chartDot struct {
	X, Y, Tip string
	Win       bool
}

const (
	chartW, chartH = 640, 190
	padL, padR     = 40.0, 112.0
	padT, padB     = 10.0, 24.0
)

func newLiveVsBacktestWinrateChart(r live_vs_backtest_winrate.Row) *winrateChart {
	c := &winrateChart{W: chartW, H: chartH, Empty: len(r.Progress) == 0 && len(r.ISProgress) == 0 && len(r.OOSProgress) == 0}
	c.Plot = chartBox{padL, padT, chartW - padL - padR, chartH - padT - padB}
	y := func(f float64) float64 { return padT + c.Plot.H*(1-f) }
	n := max(len(r.Progress), len(r.ISProgress), len(r.OOSProgress))
	// Leave room to grow: at least 12 trades wide, so the first few points do not sprawl.
	xmax := max(n, 12)
	x := func(k int) float64 {
		if xmax == 1 {
			return padL + c.Plot.W/2
		}
		return padL + c.Plot.W*float64(k-1)/float64(xmax-1)
	}
	for _, g := range []float64{0, .25, .5, .75, 1} {
		c.Grid = append(c.Grid, chartGrid{Y: fmt.Sprintf("%.1f", y(g)), Label: fmt.Sprintf("%.0f%%", g*100)})
	}
	step := 1
	for xmax/step > 12 {
		step *= 2
	}
	for k := step; k <= xmax; k += step {
		c.Ticks = append(c.Ticks, chartTick{X: fmt.Sprintf("%.1f", x(k)), Label: fmt.Sprint(k)})
	}
	x1, x2 := fmt.Sprintf("%.1f", padL), fmt.Sprintf("%.1f", padL+c.Plot.W)
	// The whole-window rate is only drawn when there is no split to draw instead.
	refs := []ref{{r.ISWinRate, "in-sample", "is"}, {r.OOSWinRate, "out-of-sample", "oos"}}
	if r.ISWinRate == nil && r.OOSWinRate == nil {
		refs = []ref{{r.AllWinRate, "backtest", "all"}}
	}
	var drawn []ref
	for _, b := range refs {
		if b.rate != nil {
			drawn = append(drawn, b)
		}
	}
	// Labels sit at their line's height, pushed down just enough that none overlap.
	slices.SortStableFunc(drawn, func(a, b ref) int { return cmp.Compare(*b.rate, *a.rate) })
	prev := -100.0
	for _, b := range drawn {
		ly := max(y(*b.rate), prev+13)
		prev = ly
		c.Lines = append(c.Lines, chartLine{Y: fmt.Sprintf("%.1f", y(*b.rate)), X1: x1, X2: x2,
			Label: fmt.Sprintf("%s %.0f%%", b.label, *b.rate*100), LabelY: fmt.Sprintf("%.1f", ly+4), Class: b.class})
	}
	curve := func(class, label string, pts []live_vs_backtest_winrate.BTPoint) {
		if len(pts) == 0 {
			return
		}
		var xy []string
		for _, p := range pts {
			xy = append(xy, fmt.Sprintf("%.1f,%.1f", x(p.N), y(p.Rate)))
		}
		last := pts[len(pts)-1]
		c.Curves = append(c.Curves, chartCurve{Class: class, Points: strings.Join(xy, " "),
			Tip: fmt.Sprintf("%s backtest: %.0f%% after %d trades", label, last.Rate*100, last.N)})
	}
	curve("is", "In-sample", r.ISProgress)
	curve("oos", "Out-of-sample", r.OOSProgress)
	var line, hi, lo []string
	for _, p := range r.Progress {
		pt := fmt.Sprintf("%.1f,%.1f", x(p.N), y(p.Rate))
		line = append(line, pt)
		hi = append(hi, fmt.Sprintf("%.1f,%.1f", x(p.N), y(p.CIHigh)))
		lo = append([]string{fmt.Sprintf("%.1f,%.1f", x(p.N), y(p.CILow))}, lo...)
		out := "loss"
		if p.Win {
			out = "win"
		}
		c.Dots = append(c.Dots, chartDot{X: fmt.Sprintf("%.1f", x(p.N)), Y: fmt.Sprintf("%.1f", y(p.Rate)), Win: p.Win,
			Tip: fmt.Sprintf("Decision %d, %s %s: %s (%+.1f%%). Win rate now %.0f%% (%d of %d), 95%% range %.0f-%.0f%%.",
				p.N, p.Symbol, p.Date, out, p.Return, p.Rate*100, p.Wins, p.N, p.CILow*100, p.CIHigh*100)})
	}
	c.Line = strings.Join(line, " ")
	if n > 1 {
		c.Band = strings.Join(append(hi, lo...), " ")
	}
	return c
}

var winrateTmpl = template.Must(template.New("live_vs_backtest_winrate").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>live_vs_backtest_winrate</title>
<style>` + pageStyle + `
  :root { --is:#2f6fed; --oos:#9a6700; --all:#8250df; }
  @media (prefers-color-scheme: dark) { :root { --is:#6ea2ff; --oos:#e0b040; --all:#b392f0; } }
  .chips { display:flex; flex-wrap:wrap; gap:8px; margin:0 0 8px; }
  td.num { min-width:96px; } td.num b { font-size:22px; line-height:1.1; display:block; }
  td.num small { color:var(--muted); display:block; }
  .mini { position:relative; height:6px; margin-top:4px; background:var(--code); border-radius:3px; }
  .mini i { position:absolute; left:0; top:0; bottom:0; border-radius:3px; background:var(--muted); opacity:.7; }
  .mini.live i { background:currentColor; opacity:.9; }
  .mini s { position:absolute; top:2px; height:2px; background:var(--fg); }
  .dash { color:var(--muted); }
  .strat h3 { margin:0 0 4px; font-size:17px; } .strat h3 a { text-decoration:none; }
  .strat p { margin:6px 0 0; }
  svg.chart { width:100%; height:auto; display:block; max-width:920px; }
  svg.chart text { fill:var(--muted); font-size:11px; }
  svg.chart .grid { stroke:var(--line); stroke-width:1; }
  svg.chart .ref { stroke-width:1.5; stroke-dasharray:5 4; opacity:.55; }
  svg.chart .curve { fill:none; stroke-width:1.8; stroke-linejoin:round; opacity:.9; }
  svg.chart .curve.is { stroke:var(--is); } svg.chart .curve.oos { stroke:var(--oos); }
  svg.chart .ref.is { stroke:var(--is); } svg.chart .ref.oos { stroke:var(--oos); } svg.chart .ref.all { stroke:var(--all); }
  svg.chart text.is { fill:var(--is); } svg.chart text.oos { fill:var(--oos); } svg.chart text.all { fill:var(--all); }
  svg.chart .band { fill:currentColor; opacity:.14; }
  svg.chart .path { fill:none; stroke:currentColor; stroke-width:2.2; stroke-linejoin:round; }
  svg.chart .dot { stroke:var(--bg); stroke-width:1.5; }
  svg.chart .dot.win { fill:var(--ok); } svg.chart .dot.loss { fill:var(--bad); }
  .c-ok { color:var(--ok); } .c-warn { color:var(--warn); } .c-bad { color:var(--bad); } .c-muted { color:var(--fg); }
  .nodata { color:var(--muted); font-size:13px; padding:20px 0; }
  .key { font-size:12px; color:var(--muted); margin:2px 0 0; }
</style></head><body><main>
<h1>Win rate: backtest vs live</h1>
<p class="sub">In-sample, out-of-sample (the held-out last 12 months, from the default backtest run) and live closed trades, side by side. Below, each strategy's live win rate after every closed trade, against the two backtest lines it should stay near. <a href="/live_vs_backtest_winrate?format=json"><code>?format=json</code></a> &middot; <a href="/strategy">strategies</a></p>
{{if .None}}<div class="card muted">No greenlit strategies and no stored results.</div>{{end}}
<div class="chips">{{range .Counts}}<span class="tag {{.Class}}">{{.Verdict}} {{.N}}</span>{{end}}</div>

{{define "recent"}}{{if .Recent}}<h2>Closed trades, last {{.Days}} days{{if .Stops}} <span class="tag bad">{{.Stops}} stop loss{{if gt .Stops 1}}es{{end}}</span>{{end}}</h2>
<div class="table-wrap"><table>
  <tr><th>Closed</th><th>Strategy</th><th>Account</th><th>Symbol</th><th>Shares</th><th>Entry</th><th>Exit</th><th>Return</th><th>P&amp;L $</th><th>How</th></tr>
  {{range .Recent}}<tr>
    <td>{{.Closed}}</td><td><a href="#{{.Strategy}}">{{.Strategy}}</a></td><td>{{.Account}}</td><td>{{.Symbol}}</td><td>{{.Qty}}</td><td>{{.Entry}}<small class="muted" style="display:block">{{.EntryNote}}</small></td><td>{{.Exit}}<small class="muted" style="display:block">{{.ExitNote}}</small></td>
    <td class="{{.Class}}"><b>{{.Return}}</b></td><td class="{{.Class}}">{{.PnL}}</td><td><span class="tag {{.Class}}">{{.Sub}}</span></td>
  </tr>{{end}}
</table></div>
{{if .Unmeasured}}<p class="sub"><span class="tag warn">not the real prices</span> {{.Unmeasured}} of these rows are still measured from the entry limit and the exit order's price, because the broker's fill prices are not recorded on them yet. The real loss is usually different (a limit often fills better than it was set, a stop fills below its trigger). Run <code>orchestrator backfill_fills</code> to record them, or see <a href="/orders">/orders</a>.</p>{{end}}
<p class="key">One row per account fill. The win rate above counts decisions: a strategy buying one symbol on one day is one decision, however many accounts it filled in, and it wins when the fills' P&amp;L adds up above zero. <a href="?days=14">Last 14 days</a> &middot; <a href="?days=1">today only</a></p>
{{else}}<p class="key">Nothing closed in the last {{.Days}} days. <a href="?days=30">Last 30 days</a></p>{{end}}

{{end}}

{{define "num"}}{{if .Has}}<b>{{.Rate}}</b><small>{{.Trade}}</small><div class="mini"><i style="width:{{.Pct}}%"></i></div>{{else}}<span class="dash">&ndash;</span>{{end}}{{end}}

{{define "table"}}<div class="table-wrap"><table>
  <tr><th>Strategy</th><th>In-sample</th><th>Out-of-sample</th><th>Backtest, whole window</th><th>Live</th><th>Verdict</th></tr>
  {{range .}}<tr>
    <td><a href="#{{.Strategy}}"><b>{{.Strategy}}</b></a>{{if .BacktestSource}}<small class="muted" style="display:block">backtest {{.BacktestSource}}</small>{{end}}</td>
    <td class="num">{{template "num" .IS}}</td>
    <td class="num">{{template "num" .OOS}}</td>
    <td class="num">{{template "num" .All}}</td>
    <td class="num {{.Class}}">{{if .Live.Has}}<b>{{.LiveWins}}/{{.LiveDecisions}}</b><small>{{.Live.Rate}} win rate{{if gt .LiveFills .LiveDecisions}}, {{.LiveFills}} account fills{{end}}</small><div class="mini live"><i style="width:{{.Live.Pct}}%"></i>{{if .Live.Lo}}<s style="left:{{.Live.Lo}}%;width:calc({{.Live.Hi}}% - {{.Live.Lo}}%)"></s>{{end}}</div>{{else}}<span class="dash">0/0, none closed yet</span>{{end}}{{if or .OpenPositions .PendingFills}}<small>{{.OpenPositions}} open</small>{{end}}</td>
    <td><span class="tag {{.Class}}">{{.Verdict}}</span></td>
  </tr>{{end}}
</table></div>{{end}}

{{define "chart"}}{{with .Chart}}
<svg class="chart" viewBox="0 0 {{.W}} {{.H}}" role="img" aria-label="Live win rate after each closed trade">
  {{range .Grid}}<line class="grid" x1="{{$.Chart.Plot.X}}" x2="{{$.Chart.W}}" y1="{{.Y}}" y2="{{.Y}}"/>{{end}}
  {{range .Grid}}<text x="{{$.Chart.Plot.X}}" y="{{.Y}}" dx="-6" dy="4" text-anchor="end">{{.Label}}</text>{{end}}
  {{range .Ticks}}<text x="{{.X}}" y="{{$.Chart.H}}" dy="-6" text-anchor="middle">{{.Label}}</text>{{end}}
  {{range .Curves}}<polyline class="curve {{.Class}}" points="{{.Points}}"><title>{{.Tip}}</title></polyline>{{end}}
  {{range .Lines}}<line class="ref {{.Class}}" x1="{{.X1}}" x2="{{.X2}}" y1="{{.Y}}" y2="{{.Y}}"/><text class="{{.Class}}" x="{{.X2}}" y="{{.LabelY}}" dx="6">{{.Label}}</text>{{end}}
  <g class="c-{{$.Class}}">
    {{if .Band}}<polygon class="band" points="{{.Band}}"/>{{end}}
    {{if .Line}}<polyline class="path" points="{{.Line}}"/>{{end}}
  </g>
  {{range .Dots}}<circle class="dot {{if .Win}}win{{else}}loss{{end}}" cx="{{.X}}" cy="{{.Y}}" r="4.5"><title>{{.Tip}}</title></circle>{{end}}
</svg>
<p class="key">x: trade number (live: decision number). Blue and amber curves: the backtest's running win rate over its in-sample and out-of-sample trades; dashed lines mark where each ended. Bold line: live win rate so far, with its 95% range; green dot a win, red a loss, hover for the trade.{{if not $.Progress}} No closed live decisions yet, so the live line starts with the first one.{{end}}</p>
{{end}}{{end}}

{{define "cards"}}{{range .}}<div class="card strat" id="{{.Strategy}}">
  <h3><a href="/status/{{.Strategy}}">{{.Strategy}}</a> <span class="tag {{.Class}}">{{.Verdict}}</span></h3>
  {{if .Chart.Empty}}<p class="muted">Nothing to chart yet: no backtest trades are stored and no live decision has closed. {{.Headline}}</p>
  {{else}}{{template "chart" .}}
  <p class="muted">{{.Headline}}</p>{{end}}
</div>{{end}}{{end}}

{{if .Greenlit}}<h2>Greenlit ({{len .Greenlit}})</h2>{{template "table" .Greenlit}}
{{template "recent" .}}
{{template "cards" .Greenlit}}{{else}}{{template "recent" .}}{{end}}
{{if .Other}}<h2>Not greenlit, but with results ({{len .Other}})</h2>{{template "table" .Other}}{{template "cards" .Other}}{{end}}
</main></body></html>`))

func nonNilTrades(t []live_vs_backtest_winrate.Trade) []live_vs_backtest_winrate.Trade {
	if t == nil {
		return []live_vs_backtest_winrate.Trade{}
	}
	return t
}

// closedAt formats a stored exit time (UTC, in several layouts the broker sync has written) in
// Eastern time, the market's clock.
func closedAt(raw string) string {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.UTC
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999 -0700 MST", "2006-01-02 15:04:05 -0700 -0700", "2006-01-02 15:04:05 -0700 MST", time.RFC3339Nano} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.In(loc).Format("Mon Jan 2, 3:04 PM ET")
		}
	}
	return raw
}

func recentLines(trades []live_vs_backtest_winrate.Trade) ([]recentLine, int, int) {
	var out []recentLine
	stops, unmeasured := 0, 0
	for _, t := range trades {
		l := recentLine{Closed: closedAt(t.ExitTime), Strategy: t.Strategy, Symbol: t.Symbol, Stop: t.StopLoss,
			Qty: fmt.Sprintf("%g", t.Quantity), Entry: fmt.Sprintf("$%.2f", t.EntryPrice), Exit: fmt.Sprintf("$%.2f", t.ExitPrice),
			EntryNote: "limit, not the fill", ExitNote: "order price, not the fill",
			Return: fmt.Sprintf("%+.1f%%", t.ReturnPct), PnL: fmt.Sprintf("%+.2f", t.PnL), Reason: t.Reason}
		if t.EntryFill > 0 {
			l.Entry, l.EntryNote = fmt.Sprintf("$%.2f", t.EntryFill), "fill"
		}
		if t.ExitFill > 0 {
			l.Exit, l.ExitNote = fmt.Sprintf("$%.2f", t.ExitFill), "fill"
		}
		if t.EntryFill <= 0 || t.ExitFill <= 0 {
			unmeasured++
		}
		if len(t.Account) > 4 {
			l.Account = "..." + t.Account[len(t.Account)-4:]
		} else {
			l.Account = t.Account
		}
		l.Sub = strings.ReplaceAll(strings.TrimPrefix(t.Status, "CLOSED_"), "_", " ")
		switch {
		case t.StopLoss:
			l.Class = "bad"
			l.Sub = "STOP LOSS"
			stops++
		case t.PnL > 0:
			l.Class = "ok"
		default:
			l.Class = "muted"
		}
		out = append(out, l)
	}
	return out, stops, unmeasured
}
