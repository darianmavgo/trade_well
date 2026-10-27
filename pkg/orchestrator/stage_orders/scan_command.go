package stage_orders

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/backtest/strategy"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/job_runs"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/scan_market"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

// ScanOptions are the inputs to the `orchestrator scan_market` CLI job.
type ScanOptions struct {
	Accounts   jobenv.Accounts
	Symbols    string  // comma-separated symbols to scan
	Strategy   string  // single strategy ID (must be greenlit)
	Strategies string  // comma-separated strategy IDs (default: every STRATEGY_ALLOWLIST entry)
	ProfitPct  float64 // override take-profit percent; 0 = the strategy's value
	MaxDays    int     // override holding window in trading days; 0 = the strategy's window
	DryRun     bool    // preview only; do not write staged orders
}

// RunScanAndStage is the CLI form of `orchestrator scan_market`: per account and
// strategy it scans for buy signals (scan_market.Run) and immediately stages
// orders for them (Run). It lives in this package rather than pkg/scan_market
// because staging depends on scan_market, not the other way round.
//
// NOTE: this is NOT the code path behind GET /cron/scan_market, which runs
// scan_market.RunSignalScan (Schwab-free, writes buy signals only) and leaves
// staging to the stage_orders job. Output goes to out; the caller decides the
// exit code from the returned error.
func RunScanAndStage(ctx context.Context, out io.Writer, env jobenv.Env, opts ScanOptions) error {
	fmt.Fprintf(out, "\n%s%s=== [Job 2] orchestrator Strategy Buy Scanner ===%s\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)
	fmt.Fprintf(out, "Mode: %s | Sizing: each strategy's AllocationPct of live account equity (no dollar-amount fallback; override holding window with -max-days, take-profit with -profit)\n", jobenv.ModeLabel(opts.DryRun))

	strategyIDs, err := strategies.ResolveRequest(opts.Strategies, opts.Strategy)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Strategies: %s%s%s\n", jobenv.ColBold, strings.Join(strategyIDs, ", "), jobenv.ColReset)
	for _, id := range strategyIDs {
		members := strategy.ParseStack(id)
		if len(members) > 1 {
			fmt.Fprintf(out, "Stack %s: primary sizes ~AllocationPct of equity first; overlays use leftover cash only (live v1 does not sell an open overlay to fund the primary).\n", id)
		} else {
			fmt.Fprintf(out, "%s: sizes to its AllocationPct of live account equity; skipped if the account has no reported balance.\n", id)
		}
	}

	targets, err := opts.Accounts.Targets()
	if err != nil {
		return err
	}

	// No stub equity: missing account rows fail loudly. Dry-run still sizes
	// against real mirrored balances (run account_state first).
	resolvedAccounts, err := jobenv.ResolveAccountsLocalOnly(env.DB, targets)
	if err != nil {
		fmt.Fprintf(out, "RESULT=FAILED reason=no_accounts\n")
		return fmt.Errorf("failed to resolve target account(s): %w", err)
	}
	fmt.Fprintf(out, "Accounts: the Turso account_state mirror only (no stub equity)\n")

	symbols := strings.Split(opts.Symbols, ",")

	started := time.Now().UTC()
	var totalScanned, totalSignals, totalStaged int
	var allRows []send_email.Row
	reported := map[string]bool{} // each strategy's scan is listed once, not once per account
	for _, acc := range resolvedAccounts {
		for _, strategyID := range strategyIDs {
			fmt.Fprintf(out, "\n%sTarget Account: %s | Strategy: %s%s\n", jobenv.ColBold, acc.AccountNumber, strategyID, jobenv.ColReset)

			members, err := strategies.ResolveGreenlit(env.DB, []string{strategyID})
			if err != nil {
				fmt.Fprintf(out, "%s[Error]%s Failed to resolve strategy %s: %v\n", jobenv.ColRed, jobenv.ColReset, strategyID, err)
				continue
			}

			scanResult, err := scan_market.Run(ctx, env.DB, env.Cfg, strategyID, members, symbols)
			if err != nil {
				fmt.Fprintf(out, "%s[Error]%s Strategy scan failed for account %s / strategy %s: %v\n", jobenv.ColRed, jobenv.ColReset, acc.AccountNumber, strategyID, err)
				continue
			}
			for _, d := range scanResult.Details {
				fmt.Fprintf(out, "  • %s\n", d)
			}
			if !reported[strategyID] {
				reported[strategyID] = true
				allRows = append(allRows, scan_market.EmailRows(scanResult.StrategyScans())...)
			}

			stageResult, err := Run(ctx, env.DB, acc, strategyID, scanResult.Signals, members, opts.MaxDays, opts.DryRun, opts.ProfitPct)
			if err != nil {
				fmt.Fprintf(out, "%s[Error]%s Order staging failed for account %s / strategy %s: %v\n", jobenv.ColRed, jobenv.ColReset, acc.AccountNumber, strategyID, err)
				if stageResult != nil {
					allRows = append(allRows, EmailRows(stageResult.Rows)...)
				}
				continue
			}

			for _, d := range stageResult.Details {
				fmt.Fprintf(out, "  • %s\n", d)
			}
			fmt.Fprintf(out, "  %sSummary:%s Scanned: %d | Buy Signals: %d | Orders Staged: %d\n",
				jobenv.ColBold, jobenv.ColReset, scanResult.SymbolsScanned, len(scanResult.Signals), stageResult.OrdersStaged)

			totalScanned += scanResult.SymbolsScanned
			totalSignals += len(scanResult.Signals)
			totalStaged += stageResult.OrdersStaged
			allRows = append(allRows, EmailRows(stageResult.Rows)...)
		}
	}

	fmt.Fprintf(out, "\n%sStrategy Scan Grand Total (%d account(s) x %d strategy/ies):%s Scanned: %d | Buy Signals: %d | Orders Staged: %d\n\n",
		jobenv.ColBold, len(resolvedAccounts), len(strategyIDs), jobenv.ColReset, totalScanned, totalSignals, totalStaged)
	if totalSignals > 0 {
		fmt.Fprintf(out, "RESULT=SIGNAL signals=%d staged=%d scanned=%d\n", totalSignals, totalStaged, totalScanned)
	} else if totalScanned > 0 {
		fmt.Fprintf(out, "RESULT=NO_SIGNAL scanned=%d\n", totalScanned)
	} else {
		fmt.Fprintf(out, "RESULT=FAILED reason=nothing_scanned\n")
	}

	// Record the run as the cron route does, so a good manual scan counts for /ready and
	// check_health (they judge the scan job by its latest job_run).
	if !opts.DryRun {
		status := job_runs.JobRunOK
		switch {
		case totalScanned == 0:
			status = job_runs.JobRunFailed
		case totalSignals == 0:
			status = job_runs.JobRunEmpty
		}
		summary := fmt.Sprintf("cli scanned=%d buy_signals=%d staged=%d strategies=%s", totalScanned, totalSignals, totalStaged, strings.Join(strategyIDs, ","))
		if _, err := job_runs.RecordJobRun(env.DB, job_runs.JobRun{Job: "scan_market", Status: status, Summary: summary,
			RowsWritten: totalSignals, StartedAt: started, FinishedAt: time.Now().UTC()}); err != nil {
			fmt.Fprintf(out, "%s[Warning]%s scan_market job_run not recorded: %v\n", jobenv.ColYellow, jobenv.ColReset, err)
		}
	}

	if totalStaged > 0 {
		fmt.Fprintf(out, "Run %sorchestrator submit_orders%s to review and submit staged orders.\n\n", jobenv.ColBold, jobenv.ColReset)
	}

	if !opts.DryRun {
		subject, body := send_email.BuildJobReport("scan_market", allRows)
		if err := send_email.SendHTMLEmail(env.DB, "scan_market", subject, body); err != nil {
			fmt.Fprintf(out, "%s[Warning]%s Failed to send scan_market report email: %v\n", jobenv.ColYellow, jobenv.ColReset, err)
		}
	}
	return nil
}
