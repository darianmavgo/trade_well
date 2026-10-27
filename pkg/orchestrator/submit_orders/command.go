package submit_orders

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
)

// CommandOptions are the inputs to the `orchestrator submit_orders` job.
type CommandOptions struct {
	Accounts jobenv.Accounts
	DryRun   bool // preview which staged orders would submit/reject without calling Schwab
}

// EmailRows converts the job's rows to the shape send_email reports on.
func EmailRows(rows []Row) []send_email.Row {
	out := make([]send_email.Row, len(rows))
	for i, r := range rows {
		out[i] = send_email.Row{Account: r.Account, Strategy: r.Strategy, Symbol: r.Symbol, Signal: r.Signal, Status: r.Status, Detail: r.Detail}
	}
	return out
}

// RunCommand is the `orchestrator submit_orders` job: re-validate and submit
// the orders stage_orders staged, per target account. Output goes to out; the
// caller decides the exit code from the returned error.
func RunCommand(ctx context.Context, out io.Writer, env jobenv.Env, opts CommandOptions) error {
	fmt.Fprintf(out, "\n%s%s=== Order Submission (Staged Orders) ===%s\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)
	jobenv.WriteMode(out, opts.DryRun)

	targets, err := opts.Accounts.Targets()
	if err != nil {
		return err
	}
	tc, resolvedAccounts, err := jobenv.ResolveAccounts(ctx, env.Cfg, env.DB, targets)
	if err != nil {
		return fmt.Errorf("failed to resolve target account(s): %w", err)
	}
	res, err := RunJob(ctx, env.DB, tc, resolvedAccounts, JobOptions{DryRun: opts.DryRun, Log: log.New(out, "", 0)})
	if res != nil {
		fmt.Fprintf(out, "\n%sOrder Submission Grand Total (%d account(s)):%s Staged Found: %d | Submitted: %d | Filled: %d | Pending Fill: %d | Rejected: %d | Errors: %d\n\n",
			jobenv.ColBold, res.Accounts, jobenv.ColReset, res.StagedOrdersFound, res.Submitted, res.Filled, res.PendingFill, res.Rejected, len(res.Errors))
		if res.EmailErr != nil {
			fmt.Fprintf(out, "%s[Warning]%s Failed to send submit_orders report email: %v\n", jobenv.ColYellow, jobenv.ColReset, res.EmailErr)
		}
	}
	return err
}
