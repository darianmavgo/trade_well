package analytics

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/backtest/models"
)

//go:embed report_template.html
var reportTemplateHTML string

type MultiStrategyHTMLData struct {
	Title          string                          `json:"title"`
	GeneratedAt    string                          `json:"generated_at"`
	Symbol         string                          `json:"symbol"`
	StartDate      string                          `json:"start_date"`
	EndDate        string                          `json:"end_date"`
	TotalDays      int                             `json:"total_days"`
	TotalYears     float64                         `json:"total_years"`
	InitialCap     float64                         `json:"initial_cap"`
	Strategies     []StrategyReportData            `json:"strategies"`
	AllDates       []string                        `json:"all_dates"`
	EquityCurves   map[string][]float64            `json:"equity_curves"`
	DrawdownCurves map[string][]float64            `json:"drawdown_curves"`
	CashFlows      map[string][]CashFlowPointEntry `json:"cash_flows,omitempty"`
	// Params are the settings of the run that made this report, shown up front.
	Params []models.ParamGroup `json:"params,omitempty"`
}

type CashFlowPointEntry struct {
	Date           string  `json:"date"`
	BuyingPower    float64 `json:"buying_power"`
	MarginDebt     float64 `json:"margin_debt"`
	MarginInterest float64 `json:"margin_interest"`
	DividendIncome float64 `json:"dividend_income"`
}

type StrategyReportData struct {
	ID     string                   `json:"id"`
	Name   string                   `json:"name"`
	Type   string                   `json:"type"`
	Report models.PerformanceReport `json:"report"`
	Trades []models.Trade           `json:"trades"`
}

// GenerateComparisonHTML creates a rich, modern, interactive HTML report with Chart.js charts.
func GenerateComparisonHTML(outputPath string, data MultiStrategyHTMLData) error {
	// The report goes where it is asked: a run's report sits in its run folder.
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return fmt.Errorf("failed to create report directory: %w", err)
	}
	partitionedOutputPath := outputPath

	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to marshal JSON data: %w", err)
	}

	replacements := map[string]string{
		"{{TITLE}}":        data.Title,
		"{{GENERATED_AT}}": data.GeneratedAt,
		"{{START_DATE}}":   data.StartDate,
		"{{END_DATE}}":     data.EndDate,
		"{{TOTAL_YEARS}}":  fmt.Sprintf("%.1f", data.TotalYears),
		"{{TOTAL_DAYS}}":   fmt.Sprintf("%d", data.TotalDays),
		"{{INITIAL_CAP}}":  fmt.Sprintf("%.2f", data.InitialCap),
		"{{JSON_DATA}}":    string(jsonBytes),
	}

	outputContent := reportTemplateHTML
	for k, v := range replacements {
		outputContent = strings.ReplaceAll(outputContent, k, v)
	}

	return os.WriteFile(partitionedOutputPath, []byte(outputContent), 0644)
}
