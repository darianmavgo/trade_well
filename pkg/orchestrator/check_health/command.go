package check_health

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/send_email"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/strategies"
)

// EmailRows converts the job's rows to the shape send_email reports on.
func EmailRows(rows []Row) []send_email.Row {
	out := make([]send_email.Row, len(rows))
	for i, r := range rows {
		out[i] = send_email.Row{Account: r.Account, Strategy: r.Strategy, Symbol: r.Symbol, Signal: r.Signal, Status: r.Status, Detail: r.Detail}
	}
	return out
}

// RunCommand is the `orchestrator check_health` job: audit every target
// account (protective orders, scheduled exits, plus every enabled check_health
// row) and email the report. Output goes to out; the caller decides the exit
// code from the returned error.
func RunCommand(ctx context.Context, out io.Writer, env jobenv.Env, acc jobenv.Accounts) error {
	fmt.Fprintf(out, "\n%s%s=== [Job 3] orchestrator Daily Account Health Guardian ===%s\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)

	// Which scheduled job did not do its job. Needs only the orchestrator database (Turso), so
	// it runs (and is reported) even when the broker or the account list is broken.
	// A diagnosis that cannot run comes back as its own finding, never as all-clear.
	pipeRows, pipeFindings, _ := PipelineRows(env.DB, time.Now().UTC())
	fmt.Fprint(out, FormatPipeline(pipeFindings))
	allRows := EmailRows(pipeRows)

	targets, err := acc.Targets()
	if err != nil {
		return err
	}
	tc, resolvedAccounts, err := jobenv.ResolveAccounts(ctx, env.Cfg, env.DB, targets)
	if err != nil {
		if len(allRows) > 0 {
			subject, body := send_email.BuildJobReport("check_health", append(allRows, send_email.Row{
				Signal: "ERROR", Status: "ERROR", Detail: fmt.Sprintf("could not resolve target account(s): %v", err)}))
			_ = send_email.SendHTMLEmail(env.DB, "check_health", subject, body)
		}
		return fmt.Errorf("failed to resolve target account(s): %w", err)
	}
	strats, err := strategies.ResolveGreenlit(env.DB, env.Cfg.StrategyAllowlist)
	if err != nil {
		return fmt.Errorf("failed to resolve greenlit strategies: %w", err)
	}
	stratsByID := strategies.ByID(strats)

	for _, a := range resolvedAccounts {
		report, err := Run(ctx, env.DB, tc, a, stratsByID)
		if err != nil {
			allRows = append(allRows, send_email.Row{
				Account: a.AccountNumber, Signal: "ERROR", Status: "ERROR",
				Detail: fmt.Sprintf("health check failed to run: %v", err),
			})
			fmt.Fprintf(out, "%s[Error]%s Health check failed for account %s: %v\n", jobenv.ColRed, jobenv.ColReset, a.AccountNumber, err)
			continue
		}

		allRows = append(allRows, EmailRows(report.Rows())...)
		fmt.Fprint(out, report.Format())
	}

	subject, body := send_email.BuildJobReport("check_health", allRows)
	if err := send_email.SendHTMLEmail(env.DB, "check_health", subject, body); err != nil {
		fmt.Fprintf(out, "%s[Warning]%s Failed to send check_health report email: %v\n", jobenv.ColYellow, jobenv.ColReset, err)
	}
	return nil
}
