package stage_orders

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

// CommandOptions are the inputs to the `orchestrator stage_orders` job.
type CommandOptions struct {
	Accounts  jobenv.Accounts
	ProfitPct float64 // override take-profit percent; 0 = the strategy's value
	MaxDays   int     // override holding window in trading days; 0 = the strategy's window
	DryRun    bool    // size and report, write nothing
}

// EmailRows converts the job's rows to the shape send_email reports on.
func EmailRows(rows []Row) []send_email.Row {
	out := make([]send_email.Row, len(rows))
	for i, r := range rows {
		out[i] = send_email.Row{Account: r.Account, Strategy: r.Strategy, Symbol: r.Symbol, Signal: r.Signal, Status: r.Status, Detail: r.Detail}
	}
	return out
}

// RunCommand is the `orchestrator stage_orders` job: size scan_market's buy
// signals against live cash and stage bracket orders. It is the same capability
// as GET /cron/stage_orders; both call RunJob. Output goes to out; the caller
// decides the exit code from the returned error.
func RunCommand(ctx context.Context, out io.Writer, env jobenv.Env, opts CommandOptions) error {
	fmt.Fprintf(out, "\n%s%s=== [Job 2a] orchestrator Order Staging ===%s\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)
	if opts.DryRun {
		fmt.Fprintf(out, "Mode: %sDRY-RUN (nothing written)%s\n", jobenv.ColGreen, jobenv.ColReset)
	} else {
		fmt.Fprintf(out, "Mode: %sLIVE (writes stage_order rows)%s\n", jobenv.ColRed, jobenv.ColReset)
	}

	targets, err := opts.Accounts.Targets()
	if err != nil {
		return err
	}
	_, accounts, err := jobenv.ResolveAccounts(ctx, env.Cfg, env.DB, targets)
	if err != nil {
		return fmt.Errorf("failed to resolve account(s): %w", err)
	}
	strats, err := strategies.ResolveGreenlit(env.DB, env.Cfg.StrategyAllowlist)
	if err != nil {
		return err
	}

	res, err := RunJob(ctx, env.DB, accounts, JobOptions{
		DryRun: opts.DryRun, MaxTradingDays: opts.MaxDays, ProfitPct: opts.ProfitPct, Members: strategies.ByID(strats),
	})
	if err != nil {
		fmt.Fprintf(out, "RESULT=FAILED\n")
		return err
	}
	for _, d := range res.Details {
		fmt.Fprintf(out, "  • %s\n", d)
	}
	fmt.Fprintf(out, "\n%sSummary:%s accounts=%d signals=%d staged=%d dismissed=%d\n", jobenv.ColBold, jobenv.ColReset, res.Accounts, res.Signals, res.Staged, res.Dismissed)
	if res.Signals == 0 {
		fmt.Fprintf(out, "RESULT=NO_SIGNAL\n")
	} else {
		fmt.Fprintf(out, "RESULT=STAGED staged=%d\n", res.Staged)
	}
	if res.Staged > 0 && !opts.DryRun {
		fmt.Fprintf(out, "Run %sorchestrator submit_orders%s to submit staged orders.\n\n", jobenv.ColBold, jobenv.ColReset)
	}
	if !opts.DryRun {
		subject, body := send_email.BuildJobReport("stage_orders", EmailRows(res.Rows))
		if err := send_email.SendHTMLEmail(env.DB, "stage_orders", subject, body); err != nil {
			fmt.Fprintf(out, "%s[Warning]%s Failed to send stage_orders report email: %v\n", jobenv.ColYellow, jobenv.ColReset, err)
		}
	}
	return nil
}

// ListStaged prints the orders stage_orders staged that await submit_orders
// (the `staged_orders` view), optionally filtered to one account.
func ListStaged(out io.Writer, db *sql.DB, account string) error {
	staged, err := GetStagedOrders(db, account)
	if err != nil {
		return fmt.Errorf("failed to get staged orders: %w", err)
	}

	fmt.Fprintf(out, "\n%s%s=== Staged Orders in Turso ===%s\n\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)
	if len(staged) == 0 {
		fmt.Fprintln(out, "No staged orders found in database.")
		fmt.Fprintln(out, "To generate staged orders, run: orchestrator scan_market")
		return nil
	}

	fmt.Fprintf(out, "%-4s | %-8s | %-8s | %-20s | %10s | %12s | %-9s | %s\n",
		"ID", "Account", "Symbol", "Strategy", "Quantity", "Entry Price", "Status", "Note")
	fmt.Fprintln(out, strings.Repeat("-", 110))
	for _, o := range staged {
		note := o.OrderID
		if o.Status == StagedOrderRejected {
			note = o.RejectReason
		}
		fmt.Fprintf(out, "%-4d | %-8s | %-8s | %-20s | %10.2f | $%11.2f | %-9s | %s\n",
			o.ID, o.AccountNumber, o.Symbol, o.StrategyName, o.Quantity, o.EntryPrice, o.Status, note)
	}
	fmt.Fprintln(out)
	return nil
}
