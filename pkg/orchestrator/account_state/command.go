package account_state

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// RunCommand is the `orchestrator account_state` job: ingest the configured
// accounts, positions, orders and transactions from the broker, then print a
// summary of what the Turso mirror now holds. Output goes to out; the caller
// decides the exit code from the returned error.
func RunCommand(ctx context.Context, out io.Writer, env jobenv.Env, acc jobenv.Accounts) error {
	fmt.Fprintf(out, "\n%s%s=== orchestrator account_state: Ingesting Complete broker Data ===%s\n", jobenv.ColBold, jobenv.ColCyan, jobenv.ColReset)

	targets, err := acc.Targets()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Accounts: %s%s%s\n", jobenv.ColBold, strings.Join(targets, ", "), jobenv.ColReset)

	tc, err := trader.NewTraderClient(ctx, env.Cfg.Schwab)
	if err != nil {
		return fmt.Errorf("failed to initialize Schwab client: %w", err)
	}

	result, err := Sync(ctx, env.DB, tc, targets)
	if err != nil {
		return err
	}
	for _, d := range result.Details {
		fmt.Fprintf(out, "%s•%s %s\n", jobenv.ColYellow, jobenv.ColReset, d)
	}

	writeSummary(out, env.DB)
	return nil
}

func writeSummary(out io.Writer, db *sql.DB) {
	report, err := Summary(db)
	if err != nil {
		fmt.Fprintf(out, "%s[Error]%s Failed to build summary report: %v\n", jobenv.ColRed, jobenv.ColReset, err)
		return
	}

	fmt.Fprintf(out, "\n%s%s=== orchestrator Ingestion Summary (Stored in Turso) ===%s\n", jobenv.ColBold, jobenv.ColGreen, jobenv.ColReset)
	for _, tc := range report.TableCounts {
		fmt.Fprintf(out, "  • %-32s : %s%d records%s\n", tc.Label, jobenv.ColBold, tc.Count, jobenv.ColReset)
	}

	if len(report.Accounts) > 0 {
		fmt.Fprintf(out, "\n%sAccounts Saved in DB:%s\n", jobenv.ColBold, jobenv.ColReset)
		fmt.Fprintf(out, "  %-12s | %-10s | %16s | %16s\n", "Account #", "Type", "Cash Balance", "Total Value")
		fmt.Fprintln(out, "  ----------------------------------------------------------------")
		for _, a := range report.Accounts {
			fmt.Fprintf(out, "  %-12s | %-10s | %16s | %16s\n",
				a.AccountNumber, a.AccountType, fmt.Sprintf("$%.2f", a.CashBalance), fmt.Sprintf("$%.2f", a.LiquidationValue))
		}
	}

	fmt.Fprintf(out, "\n%s✓ Complete data dump successfully saved to Turso%s\n\n", jobenv.ColGreen, jobenv.ColReset)
}
