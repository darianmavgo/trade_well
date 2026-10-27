package check_health

import (
	"fmt"
	"strings"
)

// ANSI color codes, matching cmd/orchestrator's presentation so callers can
// print Format()'s output directly with no color logic of their own.
const (
	colReset  = "\033[0m"
	colRed    = "\033[31m"
	colGreen  = "\033[32m"
	colYellow = "\033[33m"
	colCyan   = "\033[36m"
	colBold   = "\033[1m"
)

func pluralPositions(n int) string {
	return fmt.Sprintf("%d position(s) checked, no findings", n)
}

// Format renders r as the same plain-text/colorized report
// cmd/orchestrator's check_health command used to print inline.
func (r *Result) Format() string {
	var sb strings.Builder

	statusColor := colGreen
	if r.OverallStatus == "WARNING" {
		statusColor = colYellow
	} else if r.OverallStatus == "ACTION_REQUIRED" {
		statusColor = colRed
	}

	fmt.Fprintf(&sb, "Account: %s | Cash Available: $%.2f | Total Liquidation Value: $%.2f\n",
		r.AccountNumber, r.CashAvailable, r.LiquidationValue)
	fmt.Fprintf(&sb, "Health Status: %s%s%s (%d critical, %d warning finding(s))\n\n",
		statusColor, r.OverallStatus, colReset, r.CriticalFindings, r.WarningFindings)

	if len(r.PositionAudits) > 0 {
		fmt.Fprintf(&sb, "%sCurrent positions (priority)%s\n", colBold, colReset)
		for _, a := range r.PositionAudits {
			color := colGreen
			if a.Status == "WARNING" {
				color = colYellow
			} else if a.Status == "CRITICAL" {
				color = colRed
			}
			fmt.Fprintf(&sb, "  %s%-8s%s %-8s %s\n", color, a.Status, colReset, a.Symbol, a.Summary())
		}
		sb.WriteString("\n")
	}

	if len(r.Positions) > 0 {
		fmt.Fprintf(&sb, "  %-8s | %10s | %12s | %14s | %14s | %12s\n",
			"Symbol", "Quantity", "Avg Price", "Market Value", "Profit Taker", "Holding Window")
		sb.WriteString("  --------------------------------------------------------------------------------\n")
		for _, p := range r.Positions {
			ptStr := fmt.Sprintf("$%.2f", p.ProfitTakerPrice)
			if !p.HasProfitTaker {
				ptStr = fmt.Sprintf("%sNONE%s", colRed, colReset)
			}
			winStr := fmt.Sprintf("%d/%d days", p.DaysHeld, p.MaxDays)
			if !p.HasScheduledExit {
				winStr = fmt.Sprintf("%sUNTRACKED%s", colYellow, colReset)
			}
			fmt.Fprintf(&sb, "  %-8s | %10.2f | $%11.2f | $%13.2f | %14s | %12s\n",
				p.Symbol, p.Quantity, p.AveragePrice, p.MarketValue, ptStr, winStr)
		}
	} else {
		sb.WriteString("  No active positions found in account.\n")
	}

	if len(r.Findings) > 0 {
		fmt.Fprintf(&sb, "\n%sAudit Observations (by category):%s\n", colYellow, colReset)
		byCategory := make(map[string][]Finding)
		var order []string
		for _, f := range r.Findings {
			if _, seen := byCategory[f.Category]; !seen {
				order = append(order, f.Category)
			}
			byCategory[f.Category] = append(byCategory[f.Category], f)
		}
		for _, cat := range order {
			fmt.Fprintf(&sb, "  %s%s%s\n", colBold, cat, colReset)
			for _, f := range byCategory[cat] {
				mark := "⚠"
				color := colYellow
				if f.Severity == "CRITICAL" {
					mark = "✖"
					color = colRed
				}
				label := f.Subject
				if label == "" {
					label = f.CheckName
				}
				fmt.Fprintf(&sb, "    %s%s [%s]%s %s: %s\n", color, mark, f.CheckName, colReset, label, f.Detail)
			}
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

// FormatFindings renders findings the same way cmd/orchestrator's
// `health_checks -findings` command used to print inline.
func FormatFindings(findings []Finding) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n%s%s=== Recent Health Check Findings ===%s\n\n", colBold, colCyan, colReset)
	if len(findings) == 0 {
		sb.WriteString("No findings recorded. Run `orchestrator check_health` first.\n")
		return sb.String()
	}
	fmt.Fprintf(&sb, "%-20s | %-8s | %-25s | %-10s | %-8s | %s\n", "Run At", "Account", "Check", "Severity", "Subject", "Detail")
	sb.WriteString(strings.Repeat("-", 130) + "\n")
	for _, f := range findings {
		sevColor := colYellow
		if f.Severity == "CRITICAL" {
			sevColor = colRed
		}
		fmt.Fprintf(&sb, "%-20s | %-8s | %-25s | %s%-10s%s | %-8s | %s\n",
			f.RunAt.Format("2006-01-02 15:04"), f.AccountNumber, f.CheckName, sevColor, f.Severity, colReset, f.Subject, f.Detail)
	}
	sb.WriteString("\n")
	return sb.String()
}

// FormatChecks renders check definitions the same way cmd/orchestrator's
// `health_checks` command used to print inline.
func FormatChecks(checks []Def) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n%s%s=== Health Check Definitions (check_health table) ===%s\n\n", colBold, colCyan, colReset)
	fmt.Fprintf(&sb, "%-28s | %-16s | %-8s | %-7s | %s\n", "Name", "Category", "Severity", "Enabled", "Description")
	sb.WriteString(strings.Repeat("-", 130) + "\n")
	for _, c := range checks {
		mark := ""
		if c.Enabled {
			mark = fmt.Sprintf("%s✓%s", colGreen, colReset)
		}
		fmt.Fprintf(&sb, "%-28s | %-16s | %-8s | %-16s | %s\n", c.Name, c.Category, c.Severity, mark, c.Description)
	}
	fmt.Fprintf(&sb, "\n%d check(s) defined. Toggle with `--enable <name>` / `--disable <name>`; view results with `--findings`.\n\n", len(checks))
	return sb.String()
}

// FormatPipeline renders the pipeline diagnosis for the terminal and logs: one
// line when every scheduled job did its job, otherwise the root cause and each
// broken job.
func FormatPipeline(findings []Finding) string {
	if len(findings) == 0 {
		return "Pipeline: every scheduled job recorded a run for its last slot.\n"
	}
	var sb strings.Builder
	sb.WriteString("Pipeline: PROBLEMS FOUND\n")
	for _, f := range findings {
		fmt.Fprintf(&sb, "  [%s] %s: %s\n", f.CheckName, f.Subject, f.Detail)
	}
	return sb.String()
}
