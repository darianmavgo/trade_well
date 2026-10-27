package manage_exits

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

// CommandOptions are the inputs to the `orchestrator manage_exits` job.
type CommandOptions struct {
	Accounts jobenv.Accounts
	DryRun   bool // preview only; no exit orders are submitted and no email is sent
}

// EmailRows converts the job's rows to the shape send_email reports on.
func EmailRows(rows []Row) []send_email.Row {
	out := make([]send_email.Row, len(rows))
	for i, r := range rows {
		out[i] = send_email.Row{Account: r.Account, Strategy: r.Strategy, Symbol: r.Symbol, Signal: r.Signal, Status: r.Status, Detail: r.Detail}
	}
	return out
}

// RunCommand is the `orchestrator manage_exits` job: for every target account,
// check open positions against the opening strategy's holding window and profit
// target, and close those that are due. Output goes to out; the caller decides
// the exit code from the returned error.
func RunCommand(ctx context.Context, out io.Writer, env jobenv.Env, opts CommandOptions) error {
	fmt.Fprintf(out, "\n%s%s=== manage_exits ===%s\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)
	jobenv.WriteMode(out, opts.DryRun)

	targets, err := opts.Accounts.Targets()
	if err != nil {
		return err
	}
	tc, resolvedAccounts, err := jobenv.ResolveAccounts(ctx, env.Cfg, env.DB, targets)
	if err != nil {
		return fmt.Errorf("failed to resolve target account(s): %w", err)
	}
	strats, err := strategies.ResolveGreenlit(env.DB, env.Cfg.StrategyAllowlist)
	if err != nil {
		return fmt.Errorf("failed to resolve greenlit strategies: %w", err)
	}

	var totalChecked, totalClosedProfit, totalClosedExpiry, totalActive, totalSkippedNoPosition int
	var allRows []send_email.Row
	for _, acc := range resolvedAccounts {
		fmt.Fprintf(out, "\n%sTarget Account: %s%s\n", jobenv.ColBold, acc.AccountNumber, jobenv.ColReset)

		result, err := Run(ctx, env.DB, tc, acc, strats, time.Now().UTC(), opts.DryRun)
		if err != nil {
			fmt.Fprintf(out, "%s[Error]%s Exit manager execution failed for account %s: %v\n", jobenv.ColRed, jobenv.ColReset, acc.AccountNumber, err)
			if result != nil {
				allRows = append(allRows, EmailRows(result.Rows)...)
			}
			continue
		}

		for _, d := range result.Details {
			fmt.Fprintf(out, "  • %s\n", d)
		}
		fmt.Fprintf(out, "  %sSummary:%s Checked: %d | Closed Profit: %d | Expired/Exited: %d | Active: %d | No Position: %d\n",
			jobenv.ColBold, jobenv.ColReset, result.TotalChecked, result.ClosedProfit, result.ClosedTimeExpiry, result.StillActive, result.SkippedNoPosition)

		totalChecked += result.TotalChecked
		totalClosedProfit += result.ClosedProfit
		totalClosedExpiry += result.ClosedTimeExpiry
		totalActive += result.StillActive
		totalSkippedNoPosition += result.SkippedNoPosition
		allRows = append(allRows, EmailRows(result.Rows)...)
	}

	fmt.Fprintf(out, "\n%sExit Manager Grand Total (%d account(s)):%s Checked: %d | Closed Profit: %d | Expired/Exited: %d | Active: %d | No Position: %d\n\n",
		jobenv.ColBold, len(resolvedAccounts), jobenv.ColReset, totalChecked, totalClosedProfit, totalClosedExpiry, totalActive, totalSkippedNoPosition)

	if !opts.DryRun {
		subject, body := send_email.BuildJobReport("manage_exits", allRows)
		if err := send_email.SendHTMLEmail(env.DB, "manage_exits", subject, body); err != nil {
			fmt.Fprintf(out, "%s[Warning]%s Failed to send manage_exits report email: %v\n", jobenv.ColYellow, jobenv.ColReset, err)
		}
	}
	return nil
}

// ListActive prints the open strategy-managed trades (the `active_trades`
// view), optionally filtered to one account.
func ListActive(out io.Writer, db *sql.DB, account string) error {
	trades, err := GetOpenTrades(db, account)
	if err != nil {
		return fmt.Errorf("failed to get active trades: %w", err)
	}

	fmt.Fprintf(out, "\n%s%s=== Active Strategy Trades in the orchestrator database ===%s\n\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)
	if len(trades) == 0 {
		fmt.Fprintln(out, "No active open strategy trades found in database.")
		fmt.Fprintln(out, "To generate trade entries, run: orchestrator scan_market --dry-run")
		return nil
	}

	fmt.Fprintf(out, "%-4s | %-8s | %-8s | %10s | %12s | %14s | %12s | %10s | %-20s\n",
		"ID", "Account", "Symbol", "Quantity", "Entry Price", "Profit Target", "Days Held", "Max Window", "Exit Date")
	fmt.Fprintln(out, strings.Repeat("-", 120))
	for _, t := range trades {
		fmt.Fprintf(out, "%-4d | %-8s | %-8s | %10.2f | $%11.2f | $%13.2f | %12d | %8d d | %-10s (%s)\n",
			t.ID, t.AccountNumber, t.Symbol, t.Quantity, t.EntryPrice, t.TargetProfitPrice, t.TradingDaysHeld, t.MaxTradingDays, t.ExitDate, t.ExitDateSource)
	}
	fmt.Fprintln(out)
	return nil
}
