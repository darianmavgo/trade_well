// Package charting provides reusable interactive HTML reports with Chart.js.
package charting

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

//go:embed chart_template.html
var defaultHTMLTemplate string

func appendIdleKPI(kpis []KPICard, report models.PerformanceReport) []KPICard {
	if !report.IdleKnown || report.TotalTradingDays == 0 {
		return kpis
	}
	pct := float64(report.IdleDays) / float64(report.TotalTradingDays) * 100
	return append(kpis, KPICard{
		Label: "Idle Days",
		Value: fmt.Sprintf("%d", report.IdleDays),
		Sub:   fmt.Sprintf("%.1f%% of %d sessions with no open position", pct, report.TotalTradingDays),
		Color: "#94a3b8",
	})
}

// Series represents one equity/drawdown line on the charts.
type Series struct {
	Label       string    `json:"label"`
	Equity      []float64 `json:"equity"`
	Drawdown    []float64 `json:"drawdown"`
	Color       string    `json:"color"`
	BorderWidth float64   `json:"border_width"`
	IsDash      bool      `json:"is_dash"`
}

// ChartData holds the raw series data marshaled to JSON for Chart.js.
type ChartData struct {
	Dates  []string `json:"dates"`
	Series []Series `json:"series"`
}

// KPICard represents a hero statistic widget in the report header.
type KPICard struct {
	Label string
	Value string
	Sub   string
	Color string
}

// ComparisonTable holds matrix rows for multi-strategy comparison reports.
type ComparisonTable struct {
	Headers []string
	Rows    [][]string
}

// ReportView is passed to the HTML template renderer.
// Trades holds the completed round-trip trade log for the strategy.
type ReportView struct {
	Title           string
	Subtitle        string
	KPICards        []KPICard
	Trades          []models.Trade
	ComparisonTable *ComparisonTable
	ChartDataJSON   template.JS
	// Params are the settings of the run that made this report, shown up front.
	Params []models.ParamGroup
}

// GenerateHTML renders the Chart.js report and saves it to outputPath.
func GenerateHTML(outputPath string, view ReportView) error {
	dir := filepath.Dir(outputPath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0755)
	}

	funcMap := template.FuncMap{
		"multiply": func(a, b float64) float64 { return a * b },
	}

	tmpl, err := template.New("chart").Funcs(funcMap).Parse(defaultHTMLTemplate)
	if err != nil {
		return fmt.Errorf("failed to parse chart template: %w", err)
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create report file %s: %w", outputPath, err)
	}
	defer f.Close()

	return tmpl.Execute(f, view)
}

// FromPerformanceReport builds a ReportView from a PerformanceReport, trade log,
// and optional benchmark bars. Used by all strategy CLIs (combo, dip, gridsearch).
func FromPerformanceReport(
	title, subtitle string,
	report models.PerformanceReport,
	trades []models.Trade,
	benchmarkBars []models.Bar,
	initialCapital float64,
) ReportView {
	// Reconstruct daily equity series from closed trades for the chart.
	// (PortfolioSimulator also provides an EquityCurve directly; callers that
	// have it should use FromEquityCurve instead for a more accurate chart.)
	kpis := []KPICard{
		{
			Label: "Final Account Value",
			Value: fmt.Sprintf("$%.2f", report.FinalEquity),
			Sub:   signedMoney(report.NetProfit) + " Net Profit",
			Color: profitColor(report.NetProfit),
		},
		{
			Label: "Annualized CAGR",
			Value: fmt.Sprintf("%.2f%% / yr", report.CAGR*100),
			Sub:   fmt.Sprintf("%+.2f%% Total Return", report.TotalReturnPct),
			Color: "#38bdf8",
		},
		{
			Label: "Max Drawdown",
			Value: fmt.Sprintf("%.2f%%", report.MaxDrawdownPct*100),
			Sub:   fmt.Sprintf("Calmar Ratio: %.2f", report.CalmarRatio),
			Color: "#38bdf8",
		},
		{
			Label: "Win Rate & Trades",
			Value: fmt.Sprintf("%.1f%%", report.WinRate*100),
			Sub:   fmt.Sprintf("%d Trades (%dW / %dL) | %.1fd Avg Hold", report.TotalTrades, report.WinningTrades, report.LosingTrades, report.AvgHoldingDays),
			Color: "#f59e0b",
		},
	}
	kpis = appendIdleKPI(kpis, report)

	// Build a minimal benchmark overlay if bars provided.
	chartData := ChartData{Dates: []string{}}
	if len(benchmarkBars) > 0 {
		bmStart := benchmarkBars[0].Close
		bmPeak := initialCapital
		lastEq := initialCapital
		bmEq := make([]float64, len(benchmarkBars))
		bmDD := make([]float64, len(benchmarkBars))
		dates := make([]string, len(benchmarkBars))
		for i, b := range benchmarkBars {
			dates[i] = b.Date
			lastEq = (b.Close / bmStart) * initialCapital
			if lastEq > bmPeak {
				bmPeak = lastEq
			}
			bmEq[i] = lastEq
			bmDD[i] = (bmPeak - lastEq) / bmPeak * 100.0
		}
		chartData.Dates = dates
		chartData.Series = []Series{
			{
				Label:       "VOO Buy & Hold Benchmark",
				Equity:      bmEq,
				Drawdown:    bmDD,
				Color:       "#a855f7",
				BorderWidth: 1.8,
				IsDash:      true,
			},
		}
	}

	jsonData, _ := json.Marshal(chartData)

	return ReportView{
		Title:         title,
		Subtitle:      subtitle,
		KPICards:      kpis,
		Trades:        trades,
		ChartDataJSON: template.JS(string(jsonData)),
	}
}

// FromEquityCurve builds a ReportView from a full daily equity curve (as returned
// by PortfolioSimulator.Run), providing accurate mark-to-market charting.
func FromEquityCurve(
	title, subtitle string,
	report models.PerformanceReport,
	trades []models.Trade,
	curve []models.DailyEquityPoint,
	benchmarkBars []models.Bar,
	initialCapital float64,
) ReportView {
	kpis := []KPICard{
		{Label: "Final Account Value", Value: fmt.Sprintf("$%.2f", report.FinalEquity), Sub: signedMoney(report.NetProfit) + " Net Profit", Color: profitColor(report.NetProfit)},
		{Label: "Annualized CAGR", Value: fmt.Sprintf("%.2f%% / yr", report.CAGR*100), Sub: fmt.Sprintf("%+.2f%% Total Return", report.TotalReturnPct), Color: "#38bdf8"},
		{Label: "Max Drawdown", Value: fmt.Sprintf("%.2f%%", report.MaxDrawdownPct*100), Sub: fmt.Sprintf("Calmar Ratio: %.2f", report.CalmarRatio), Color: "#38bdf8"},
		{Label: "Win Rate & Trades", Value: fmt.Sprintf("%.1f%%", report.WinRate*100), Sub: fmt.Sprintf("%d Trades (%dW / %dL) | %.1fd Avg Hold", report.TotalTrades, report.WinningTrades, report.LosingTrades, report.AvgHoldingDays), Color: "#f59e0b"},
	}
	kpis = appendIdleKPI(kpis, report)

	dates := make([]string, len(curve))
	stratEq := make([]float64, len(curve))
	stratDD := make([]float64, len(curve))
	for i, pt := range curve {
		dates[i] = pt.Date
		stratEq[i] = pt.TotalEquity
		stratDD[i] = pt.DrawdownPct * 100.0
	}

	series := []Series{{
		Label:       "Strategy",
		Equity:      stratEq,
		Drawdown:    stratDD,
		Color:       profitColor(report.NetProfit),
		BorderWidth: 2.8,
	}}

	// Add benchmark if provided
	if len(benchmarkBars) > 0 {
		bmByDate := make(map[string]float64, len(benchmarkBars))
		for _, b := range benchmarkBars {
			bmByDate[b.Date] = b.Close
		}
		bmStart := benchmarkBars[0].Close
		bmPeak := initialCapital
		lastEq := initialCapital
		bmEq := make([]float64, len(dates))
		bmDD := make([]float64, len(dates))
		for i, d := range dates {
			if price, ok := bmByDate[d]; ok {
				lastEq = (price / bmStart) * initialCapital
			}
			if lastEq > bmPeak {
				bmPeak = lastEq
			}
			bmEq[i] = lastEq
			bmDD[i] = (bmPeak - lastEq) / bmPeak * 100.0
		}
		series = append(series, Series{
			Label:       "VOO Buy & Hold Benchmark",
			Equity:      bmEq,
			Drawdown:    bmDD,
			Color:       "#a855f7",
			BorderWidth: 1.8,
			IsDash:      true,
		})
	}

	jsonData, _ := json.Marshal(ChartData{Dates: dates, Series: series})

	return ReportView{
		Title:         title,
		Subtitle:      subtitle,
		KPICards:      kpis,
		Trades:        trades,
		ChartDataJSON: template.JS(string(jsonData)),
	}
}

// FromMultiReports builds a comparison ReportView for multiple strategy runs
// (e.g. grid search results). Each result is identified by a label string.
type MultiResult struct {
	Label      string
	Report     models.PerformanceReport
	DailyCurve []models.DailyEquityPoint
}

func FromMultiReports(title, subtitle string, results []MultiResult, benchmarkBars []models.Bar, initialCapital float64) ReportView {
	if len(results) == 0 {
		return ReportView{Title: title, Subtitle: subtitle}
	}

	palette := []string{
		"#38bdf8", "#0ea5e9", "#10b981", "#059669", "#f59e0b",
		"#d97706", "#f43f5e", "#e11d48", "#a855f7", "#6366f1",
	}

	// Use dates from first result
	dates := make([]string, len(results[0].DailyCurve))
	for i, pt := range results[0].DailyCurve {
		dates[i] = pt.Date
	}

	var seriesList []Series

	// Benchmark
	if len(benchmarkBars) > 0 {
		bmByDate := make(map[string]float64, len(benchmarkBars))
		for _, b := range benchmarkBars {
			bmByDate[b.Date] = b.Close
		}
		bmStart := benchmarkBars[0].Close
		bmPeak := initialCapital
		lastEq := initialCapital
		bmEq := make([]float64, len(dates))
		bmDD := make([]float64, len(dates))
		for i, d := range dates {
			if price, ok := bmByDate[d]; ok {
				lastEq = (price / bmStart) * initialCapital
			}
			if lastEq > bmPeak {
				bmPeak = lastEq
			}
			bmEq[i] = lastEq
			bmDD[i] = (bmPeak - lastEq) / bmPeak * 100.0
		}
		seriesList = append(seriesList, Series{Label: "Benchmark Buy & Hold", Equity: bmEq, Drawdown: bmDD, Color: "#94a3b8", BorderWidth: 1.8, IsDash: true})
	}

	for idx, r := range results {
		eq := make([]float64, len(dates))
		dd := make([]float64, len(dates))
		for i, pt := range r.DailyCurve {
			if i < len(dates) {
				eq[i] = pt.TotalEquity
				dd[i] = pt.DrawdownPct * 100.0
			}
		}
		seriesList = append(seriesList, Series{Label: r.Label, Equity: eq, Drawdown: dd, Color: palette[idx%len(palette)], BorderWidth: 2.2})
	}

	jsonData, _ := json.Marshal(ChartData{Dates: dates, Series: seriesList})

	tableHeaders := []string{"Rank", "Configuration", "Ending Capital", "Net Profit", "CAGR", "Max DD", "Calmar", "Win Rate", "Trades", "Idle Days"}
	tableRows := make([][]string, len(results))
	for i, r := range results {
		idle := "—"
		if r.Report.IdleKnown {
			idle = fmt.Sprintf("%d", r.Report.IdleDays)
		}
		tableRows[i] = []string{
			fmt.Sprintf("#%d", i+1),
			r.Label,
			fmt.Sprintf("$%.2f", r.Report.FinalEquity),
			signedMoney(r.Report.NetProfit),
			fmt.Sprintf("%.2f%% / yr", r.Report.CAGR*100),
			fmt.Sprintf("-%.2f%%", r.Report.MaxDrawdownPct*100),
			fmt.Sprintf("⭐ %.2f", r.Report.CalmarRatio),
			fmt.Sprintf("%.1f%%", r.Report.WinRate*100),
			fmt.Sprintf("%d", r.Report.TotalTrades),
			idle,
		}
	}

	return ReportView{
		Title:           title,
		Subtitle:        subtitle,
		ComparisonTable: &ComparisonTable{Headers: tableHeaders, Rows: tableRows},
		ChartDataJSON:   template.JS(string(jsonData)),
	}
}

// signedMoney formats a dollar amount with its sign: +$1.00 or -$1.00.
func signedMoney(v float64) string {
	if v < 0 {
		return fmt.Sprintf("-$%.2f", -v)
	}
	return fmt.Sprintf("+$%.2f", v)
}

// profitColor is green for a profit and red for a loss.
func profitColor(v float64) string {
	if v < 0 {
		return "#ef4444"
	}
	return "#10b981"
}
