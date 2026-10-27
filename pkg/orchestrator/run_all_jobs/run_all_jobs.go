// Package run_all_jobs runs the full daily suite in sequence: check_health,
// manage_exits, scan_market (scan + stage), submit_orders.
package run_all_jobs

import (
	"context"
	"fmt"
	"io"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/check_health"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/manage_exits"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/stage_orders"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/submit_orders"
)

// Options are the inputs to the `orchestrator run_all_jobs` command.
type Options struct {
	Accounts   jobenv.Accounts
	Symbols    string  // watchlist symbols for the scan
	Strategy   string  // single strategy ID (must be greenlit)
	Strategies string  // comma-separated strategy IDs (default: every STRATEGY_ALLOWLIST entry)
	ProfitPct  float64 // override take-profit percent; 0 = the strategy's value
	MaxDays    int     // override holding window in trading days; 0 = the strategy's window
	Live       bool    // live execution; false previews every step
}

// Run executes the suite in order and stops at the first step that fails, as
// the CLI always has. Output goes to out; the caller decides the exit code from
// the returned error.
func Run(ctx context.Context, out io.Writer, env jobenv.Env, opts Options) error {
	fmt.Fprintf(out, "\n%s%s========================================================%s\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)
	fmt.Fprintf(out, "%s%s  schwaber: Executing Full Automated Daily Trading Suite%s\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)
	fmt.Fprintf(out, "%s%s========================================================%s\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)

	dryRun := !opts.Live

	if err := check_health.RunCommand(ctx, out, env, opts.Accounts); err != nil {
		return fmt.Errorf("check_health: %w", err)
	}
	if err := manage_exits.RunCommand(ctx, out, env, manage_exits.CommandOptions{Accounts: opts.Accounts, DryRun: dryRun}); err != nil {
		return fmt.Errorf("manage_exits: %w", err)
	}

	scan := stage_orders.ScanOptions{
		Accounts: opts.Accounts, Symbols: opts.Symbols, Strategy: opts.Strategy, Strategies: opts.Strategies,
		ProfitPct: opts.ProfitPct, MaxDays: opts.MaxDays, DryRun: dryRun,
	}
	if scan.Strategies != "" { // -strategies wins over -strategy, as before
		scan.Strategy = ""
	}
	if err := stage_orders.RunScanAndStage(ctx, out, env, scan); err != nil {
		return fmt.Errorf("scan_market: %w", err)
	}
	if err := submit_orders.RunCommand(ctx, out, env, submit_orders.CommandOptions{Accounts: opts.Accounts, DryRun: dryRun}); err != nil {
		return fmt.Errorf("submit_orders: %w", err)
	}
	return nil
}
