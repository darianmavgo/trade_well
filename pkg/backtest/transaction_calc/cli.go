package transaction_calc

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/analytics"
	"github.com/darianmavgo/trade_well/pkg/backtest/appenv"
	"github.com/darianmavgo/trade_well/pkg/backtest/cliutils"
)

// Main is the CLI entry point.
//
//	transaction_calc -in file.csv         trade report from an IBKR export (HTML)
//	transaction_calc account -in file.csv reconstruct the account day by day into SQLite
func Main() {
	mode := cliutils.PopSubcommand(map[string]string{"account": "account"})
	var inPath string
	flag.StringVar(&inPath, "in", "", "Path to IBKR CSV file")
	if mode == "account" {
		mainAccount(&inPath)
		return
	}
	flag.Parse()
	if inPath == "" {
		log.Fatal("Must provide -in path/to/csv")
	}
	path, err := TradeReport(inPath)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Generated report at: %s\n", path)
}

func mainAccount(inPath *string) {
	cfg := AccountConfig{Out: os.Stdout}
	flag.StringVar(&cfg.Claim, "claim", "", "Tab-separated claimed performance table (Timeframe, Total, CAR, Max Drawdown, Calmar Ratio, Avg CAR, Avg MDD, Avg Trades/Year)")
	flag.StringVar(&cfg.DB, "db", "", "SQLite file that receives ibkr_transactions, claimed_performance and the account tables")
	flag.StringVar(&cfg.MarketDB, "market-db", appenv.MarketDB(), "Market database holding the daily bars")
	flag.StringVar(&cfg.Calendar, "calendar", "VOO", "Symbol whose daily bars define the trading days")
	flag.Parse()
	cfg.CSV = *inPath
	if cfg.CSV == "" || cfg.DB == "" {
		log.Fatal("account needs -in file.csv and -db file.db")
	}
	if err := Account(cfg); err != nil {
		log.Fatal(err)
	}
}

// TradeReport turns an IBKR Transaction History CSV into the backtester's HTML
// performance report and returns the path it wrote.
func TradeReport(inPath string) (string, error) {
	trades, eqPoints, initialCash, err := ParseIBKR(inPath)
	if err != nil {
		return "", fmt.Errorf("parse CSV: %w", err)
	}

	if len(trades) == 0 {
		fmt.Fprintln(os.Stderr, "No matched trades found in CSV.")
	}

	report := analytics.CalculatePerformanceMetrics(initialCash, trades, eqPoints)

	baseName := strings.TrimSuffix(filepath.Base(inPath), filepath.Ext(inPath))
	outPath := appenv.ReportFile(baseName + ".html")

	var allDates []string
	var eqCurve []float64
	var ddCurve []float64

	highWaterMark := initialCash
	for _, p := range eqPoints {
		allDates = append(allDates, p.Date)
		eqCurve = append(eqCurve, p.TotalEquity)
		if p.TotalEquity > highWaterMark {
			highWaterMark = p.TotalEquity
		}
		dd := 0.0
		if highWaterMark > 0 {
			dd = (p.TotalEquity - highWaterMark) / highWaterMark
		}
		ddCurve = append(ddCurve, dd)
	}

	startDate := ""
	endDate := ""
	if len(allDates) > 0 {
		startDate = allDates[0]
		endDate = allDates[len(allDates)-1]
	}

	htmlData := analytics.MultiStrategyHTMLData{
		Title:       fmt.Sprintf("IBKR Live Account: %s", baseName),
		GeneratedAt: time.Now().Format(time.RFC3339),
		Symbol:      "PORTFOLIO",
		StartDate:   startDate,
		EndDate:     endDate,
		TotalDays:   len(allDates),
		TotalYears:  float64(len(allDates)) / 252.0,
		InitialCap:  initialCash,
		Strategies: []analytics.StrategyReportData{
			{
				ID:     "live",
				Name:   "Live IBKR Trades",
				Type:   "live",
				Report: report,
				Trades: trades,
			},
		},
		AllDates: allDates,
		EquityCurves: map[string][]float64{
			"live": eqCurve,
		},
		DrawdownCurves: map[string][]float64{
			"live": ddCurve,
		},
	}

	today := time.Now().Format("2006-01-02")
	actualOutPath := filepath.Join(filepath.Dir(outPath), today, filepath.Base(outPath))

	if err := analytics.GenerateComparisonHTML(outPath, htmlData); err != nil {
		return "", fmt.Errorf("generate HTML: %w", err)
	}

	return actualOutPath, nil
}
