package check_health

import (
	"database/sql"
	"fmt"
	"io"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/jobenv"
)

// ChecksOptions are the inputs to the `orchestrator health_checks` command.
type ChecksOptions struct {
	Account      string // filter recent findings to this account (with ShowFindings)
	Enable       string // enable the health check with this name
	Disable      string // disable the health check with this name
	ShowFindings bool   // show recent findings instead of the check definitions
	Limit        int    // max rows with ShowFindings
}

// RunChecksCommand lists/enables/disables the data-driven checks in the
// check_health table, or shows recent violations from check_health_finding.
// check_health itself always runs every enabled row, in addition to its built-in
// live-broker-data checks; this is how an operator manages that set without a
// code change or redeploy.
func RunChecksCommand(out io.Writer, db *sql.DB, opts ChecksOptions) error {
	if opts.Enable != "" {
		if err := SetEnabled(db, opts.Enable, true); err != nil {
			return err
		}
		fmt.Fprintf(out, "%sEnabled%s health check %s%s%s\n", jobenv.ColGreen, jobenv.ColReset, jobenv.ColBold, opts.Enable, jobenv.ColReset)
		return nil
	}
	if opts.Disable != "" {
		if err := SetEnabled(db, opts.Disable, false); err != nil {
			return err
		}
		fmt.Fprintf(out, "%sDisabled%s health check %s%s%s\n", jobenv.ColYellow, jobenv.ColReset, jobenv.ColBold, opts.Disable, jobenv.ColReset)
		return nil
	}

	if opts.ShowFindings {
		findings, err := GetRecentFindings(db, opts.Account, opts.Limit)
		if err != nil {
			return fmt.Errorf("failed to get health check findings: %w", err)
		}
		fmt.Fprint(out, FormatFindings(findings))
		return nil
	}

	checks, err := ListChecks(db)
	if err != nil {
		return fmt.Errorf("failed to list health checks: %w", err)
	}
	fmt.Fprint(out, FormatChecks(checks))
	return nil
}
