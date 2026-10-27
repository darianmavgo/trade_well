package park_sweep

import (
	"fmt"
	"html/template"
	"os"
	"sort"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/storage"
	"github.com/jmoiron/sqlx"
)

const reportSchema = `
CREATE TABLE report (
	id INTEGER PRIMARY KEY CHECK (id = 1),
	generated_at TEXT NOT NULL,
	source_db TEXT NOT NULL,
	park_symbol TEXT NOT NULL,
	start_date TEXT NOT NULL,
	end_date TEXT NOT NULL,
	capital REAL NOT NULL,
	bar_count INTEGER NOT NULL,
	hold_final_equity REAL NOT NULL,
	hold_cagr REAL NOT NULL,
	hold_max_drawdown_pct REAL NOT NULL,
	hold_sharpe REAL NOT NULL,
	hold_dividends REAL NOT NULL
);
CREATE TABLE strategy_result (
	strategy_id TEXT PRIMARY KEY,
	kind TEXT NOT NULL,
	status TEXT NOT NULL,
	signal_symbol TEXT NOT NULL,
	trade_symbol TEXT NOT NULL,
	allocation_pct REAL NOT NULL,
	final_equity REAL,
	cagr REAL,
	max_drawdown_pct REAL,
	sharpe REAL,
	total_trades INTEGER,
	winning_trades INTEGER,
	losing_trades INTEGER,
	win_rate REAL,
	idle_days INTEGER,
	avg_park_weight REAL,
	dividends REAL,
	sleeve_net REAL,
	park_contribution REAL,
	edge_vs_hold REAL,
	error TEXT
);
CREATE INDEX idx_strategy_result_kind_edge ON strategy_result(kind, edge_vs_hold);
`

// WriteReport copies the sweep into a snapshot database and writes an HTML page.
func WriteReport(sweepPath, reportPath, htmlPath string, ids []string) error {
	sweep, err := Open(sweepPath)
	if err != nil {
		return err
	}
	defer sweep.Close()
	view, err := loadReport(sweep, sweepPath, ids)
	if err != nil {
		return err
	}
	if err := writeReportDB(reportPath, view); err != nil {
		return err
	}
	return writeReportHTML(htmlPath, view)
}

type reportRow struct {
	StrategyID       string  `db:"strategy_id"`
	Kind             string  `db:"kind"`
	Status           string  `db:"status"`
	SignalSymbol     string  `db:"signal_symbol"`
	TradeSymbol      string  `db:"trade_symbol"`
	AllocationPct    float64 `db:"allocation_pct"`
	FinalEquity      float64 `db:"final_equity"`
	CAGR             float64 `db:"cagr"`
	MaxDrawdownPct   float64 `db:"max_drawdown_pct"`
	Sharpe           float64 `db:"sharpe"`
	TotalTrades      int     `db:"total_trades"`
	WinningTrades    int     `db:"winning_trades"`
	LosingTrades     int     `db:"losing_trades"`
	WinRate          float64 `db:"win_rate"`
	IdleDays         int     `db:"idle_days"`
	AvgParkWeight    float64 `db:"avg_park_weight"`
	Dividends        float64 `db:"dividends"`
	SleeveNet        float64 `db:"sleeve_net"`
	ParkContribution float64 `db:"park_contribution"`
	EdgeVsHold       float64 `db:"edge_vs_hold"`
	Error            string  `db:"error"`
}

type reportView struct {
	GeneratedAt   string
	SourceDB      string
	ParkSymbol    string
	Start         string
	End           string
	Capital       float64
	BarCount      int
	HoldEquity    float64
	HoldCAGR      float64
	HoldDD        float64
	HoldSharpe    float64
	HoldDividends float64
	Rows          []reportRow
	Streaks       []reportRow
	MarkovTop     []reportRow
	Beat          int
	Trailed       int
	MedianEdge    float64
	Done          int
	Skipped       int
	Failed        int
}

func loadReport(sweep *sqlx.DB, source string, ids []string) (reportView, error) {
	cfg, err := loadConfig(sweep)
	if err != nil {
		return reportView{}, err
	}
	var hold struct {
		Final     float64 `db:"final_equity"`
		CAGR      float64 `db:"cagr"`
		DD        float64 `db:"max_drawdown_pct"`
		Sharpe    float64 `db:"sharpe"`
		Dividends float64 `db:"dividends"`
		Bars      int     `db:"bar_count"`
	}
	if err := sweep.Get(&hold, `
		SELECT final_equity, cagr, max_drawdown_pct, sharpe, dividends, bar_count
		FROM park_asset WHERE symbol = ?`, cfg.ParkSymbol); err != nil {
		return reportView{}, fmt.Errorf("park_asset: %w", err)
	}
	allocExpr := `s.allocation_pct`
	args := []interface{}{}
	if cfg.AllocationPct.Valid {
		allocExpr = `?`
		args = append(args, cfg.AllocationPct.Float64)
	}
	in, inArgs := inClause("r.strategy_id", ids)
	q := fmt.Sprintf(`
		SELECT s.strategy_id, s.kind, r.status, s.signal_symbol, s.trade_symbol,
		       %s AS allocation_pct,
		       COALESCE(r.final_equity, 0) AS final_equity,
		       COALESCE(r.cagr, 0) AS cagr,
		       COALESCE(r.max_drawdown_pct, 0) AS max_drawdown_pct,
		       COALESCE(r.sharpe, 0) AS sharpe,
		       COALESCE(r.total_trades, 0) AS total_trades,
		       COALESCE(r.winning_trades, 0) AS winning_trades,
		       COALESCE(r.losing_trades, 0) AS losing_trades,
		       COALESCE(r.win_rate, 0) AS win_rate,
		       COALESCE(r.idle_days, 0) AS idle_days,
		       COALESCE(r.avg_park_weight, 0) AS avg_park_weight,
		       COALESCE(r.dividends, 0) AS dividends,
		       COALESCE(r.sleeve_net, 0) AS sleeve_net,
		       COALESCE(r.park_contribution, 0) AS park_contribution,
		       COALESCE(r.edge_vs_googl, 0) AS edge_vs_hold,
		       COALESCE(r.error, '') AS error
		FROM strategy_run r
		JOIN sweep_strategy s ON s.strategy_id = r.strategy_id
		WHERE 1 = 1`+in+`
		ORDER BY s.kind, r.edge_vs_googl DESC`, allocExpr)
	args = append(args, inArgs...)
	var rows []reportRow
	if err := sweep.Select(&rows, q, args...); err != nil {
		return reportView{}, err
	}
	view := reportView{
		GeneratedAt:   time.Now().Format("2006-01-02 15:04:05 MST"),
		SourceDB:      source,
		ParkSymbol:    cfg.ParkSymbol,
		Start:         cfg.StartDate,
		End:           cfg.EndDate,
		Capital:       cfg.Capital,
		BarCount:      hold.Bars,
		HoldEquity:    hold.Final,
		HoldCAGR:      hold.CAGR,
		HoldDD:        hold.DD,
		HoldSharpe:    hold.Sharpe,
		HoldDividends: hold.Dividends,
		Rows:          rows,
	}
	var edges []float64
	for _, r := range rows {
		switch r.Status {
		case "done":
			view.Done++
		case "skipped":
			view.Skipped++
		case "failed":
			view.Failed++
		}
		if r.Status != "done" || r.Kind != "markov" {
			if r.Status == "done" && r.Kind == "streak" {
				view.Streaks = append(view.Streaks, r)
			}
			continue
		}
		edges = append(edges, r.EdgeVsHold)
		if r.EdgeVsHold > 0 {
			view.Beat++
		} else {
			view.Trailed++
		}
		if len(view.MarkovTop) < 25 {
			view.MarkovTop = append(view.MarkovTop, r)
		}
	}
	sort.Float64s(edges)
	if n := len(edges); n > 0 {
		if n%2 == 1 {
			view.MedianEdge = edges[n/2]
		} else {
			view.MedianEdge = (edges[n/2-1] + edges[n/2]) / 2
		}
	}
	return view, nil
}

func writeReportDB(path string, view reportView) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.Exec(reportSchema); err != nil {
		return err
	}
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO report (
			id, generated_at, source_db, park_symbol, start_date, end_date, capital,
			bar_count, hold_final_equity, hold_cagr, hold_max_drawdown_pct, hold_sharpe, hold_dividends
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		view.GeneratedAt, view.SourceDB, view.ParkSymbol, view.Start, view.End, view.Capital,
		view.BarCount, view.HoldEquity, view.HoldCAGR, view.HoldDD, view.HoldSharpe, view.HoldDividends); err != nil {
		return err
	}
	for _, r := range view.Rows {
		if _, err := tx.NamedExec(`
			INSERT INTO strategy_result (
				strategy_id, kind, status, signal_symbol, trade_symbol, allocation_pct,
				final_equity, cagr, max_drawdown_pct, sharpe, total_trades,
				winning_trades, losing_trades, win_rate, idle_days, avg_park_weight,
				dividends, sleeve_net, park_contribution, edge_vs_hold, error
			) VALUES (
				:strategy_id, :kind, :status, :signal_symbol, :trade_symbol, :allocation_pct,
				:final_equity, :cagr, :max_drawdown_pct, :sharpe, :total_trades,
				:winning_trades, :losing_trades, :win_rate, :idle_days, :avg_park_weight,
				:dividends, :sleeve_net, :park_contribution, :edge_vs_hold, :error
			)`, r); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func writeReportHTML(path string, view reportView) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	tmpl, err := template.New("park").Funcs(template.FuncMap{
		"mulf": func(a, b float64) float64 { return a * b },
	}).Parse(reportHTML)
	if err != nil {
		return err
	}
	return tmpl.Execute(f, view)
}

const reportHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.ParkSymbol}} park sweep</title>
<style>
  :root { --bg:#0b0f19; --card:#131b2e; --line:#22304d; --text:#f1f5f9; --muted:#94a3b8; --green:#34d399; --red:#f87171; --blue:#38bdf8; }
  body { margin:0; background:var(--bg); color:var(--text); font:14px/1.45 ui-sans-serif, system-ui, sans-serif; }
  main { max-width:1200px; margin:0 auto; padding:28px 20px 64px; }
  h1 { font-size:28px; margin:0 0 6px; }
  h2 { font-size:18px; margin:28px 0 10px; }
  p, .sub { color:var(--muted); }
  .kpis { display:grid; grid-template-columns:repeat(auto-fit,minmax(180px,1fr)); gap:12px; margin:18px 0; }
  .kpi { background:var(--card); border:1px solid var(--line); border-radius:10px; padding:14px 16px; }
  .kpi b { display:block; font-size:20px; color:var(--text); margin-top:4px; }
  table { width:100%; border-collapse:collapse; background:var(--card); border:1px solid var(--line); border-radius:10px; overflow:hidden; }
  th, td { padding:8px 10px; text-align:right; white-space:nowrap; border-bottom:1px solid var(--line); }
  th:first-child, td:first-child, th:nth-child(2), td:nth-child(2) { text-align:left; }
  th { color:var(--muted); font-weight:600; font-size:12px; }
  td.pos { color:var(--green); }
  td.neg { color:var(--red); }
</style>
</head>
<body>
<main>
  <h1>Park sweep: {{.ParkSymbol}}</h1>
  <p class="sub">{{.Start}} through {{.End}} · ${{printf "%.0f" .Capital}} start · {{.BarCount}} sessions · written {{.GeneratedAt}}</p>
  <div class="kpis">
    <div class="kpi">{{.ParkSymbol}} buy and hold<b>${{printf "%.0f" .HoldEquity}}</b><span class="sub">{{printf "%.2f%%" (mulf .HoldCAGR 100)}} CAGR · {{printf "%.2f%%" (mulf .HoldDD 100)}} max DD · Sharpe {{printf "%.2f" .HoldSharpe}}</span></div>
    <div class="kpi">Done<b>{{.Done}}</b><span class="sub">{{.Skipped}} skipped · {{.Failed}} failed</span></div>
    <div class="kpi">Markov vs hold<b>{{.Beat}} ahead</b><span class="sub">{{.Trailed}} behind · median edge ${{printf "%.0f" .MedianEdge}}</span></div>
  </div>
  <p>Edge is ending equity minus the {{.ParkSymbol}} buy-and-hold equity. Allocation is the strategy row's own allocation. Every row is in the report database.</p>

  <h2>Streaks</h2>
  <table>
    <thead><tr><th>Strategy</th><th>Alloc</th><th>Equity</th><th>CAGR</th><th>Max DD</th><th>Sharpe</th><th>Trades</th><th>Win</th><th>Sleeve</th><th>Park</th><th>Edge</th></tr></thead>
    <tbody>
    {{range .Streaks}}
      <tr>
        <td>{{.StrategyID}}</td><td>{{printf "%.0f%%" (mulf .AllocationPct 100)}}</td>
        <td>${{printf "%.0f" .FinalEquity}}</td><td>{{printf "%.2f%%" (mulf .CAGR 100)}}</td>
        <td>{{printf "%.2f%%" (mulf .MaxDrawdownPct 100)}}</td><td>{{printf "%.2f" .Sharpe}}</td>
        <td>{{.TotalTrades}}</td><td>{{printf "%.1f%%" (mulf .WinRate 100)}}</td>
        <td>${{printf "%.0f" .SleeveNet}}</td><td>${{printf "%.0f" .ParkContribution}}</td>
        <td class="{{if gt .EdgeVsHold 0.0}}pos{{else}}neg{{end}}">${{printf "%.0f" .EdgeVsHold}}</td>
      </tr>
    {{end}}
    </tbody>
  </table>

  <h2>Markov, top 25 by edge</h2>
  <table>
    <thead><tr><th>Strategy</th><th>Alloc</th><th>Equity</th><th>CAGR</th><th>Max DD</th><th>Sharpe</th><th>Trades</th><th>Win</th><th>Sleeve</th><th>Park</th><th>Edge</th></tr></thead>
    <tbody>
    {{range .MarkovTop}}
      <tr>
        <td>{{.StrategyID}}</td><td>{{printf "%.0f%%" (mulf .AllocationPct 100)}}</td>
        <td>${{printf "%.0f" .FinalEquity}}</td><td>{{printf "%.2f%%" (mulf .CAGR 100)}}</td>
        <td>{{printf "%.2f%%" (mulf .MaxDrawdownPct 100)}}</td><td>{{printf "%.2f" .Sharpe}}</td>
        <td>{{.TotalTrades}}</td><td>{{printf "%.1f%%" (mulf .WinRate 100)}}</td>
        <td>${{printf "%.0f" .SleeveNet}}</td><td>${{printf "%.0f" .ParkContribution}}</td>
        <td class="{{if gt .EdgeVsHold 0.0}}pos{{else}}neg{{end}}">${{printf "%.0f" .EdgeVsHold}}</td>
      </tr>
    {{end}}
    </tbody>
  </table>
</main>
</body>
</html>
`
