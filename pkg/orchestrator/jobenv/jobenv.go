// Package jobenv holds what every CLI job command shares: the config+DB
// environment, terminal colors, account-target resolution and the "already
// printed" error. It is a leaf library: it must not import any job package,
// so every pkg/<job> can import it.
package jobenv

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/account_allowlist"
	"github.com/darianmavgo/trade_well/pkg/orchestrator/appconfig"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// ANSI colors used by CLI job output.
const (
	ColReset  = "\033[0m"
	ColRed    = "\033[31m"
	ColGreen  = "\033[32m"
	ColYellow = "\033[33m"
	ColBlue   = "\033[34m"
	ColCyan   = "\033[36m"
	ColBold   = "\033[1m"
	ColDim    = "\033[2m"
)

// ErrReported means the command already told the user what went wrong (usage
// text, a hint, ...). The caller should exit non-zero without printing it again.
var ErrReported = errors.New("already reported")

// Env is the config and open database a job command runs against. The caller
// (cmd/orchestrator, or an HTTP handler) builds it and owns closing DB.
type Env struct {
	Cfg *appconfig.Config
	DB  *sql.DB
}

// Accounts carries the --account / --accounts flag values.
type Accounts struct {
	Account  string // single account number
	Accounts string // comma-separated, max account_allowlist.MaxAccounts
}

// Targets returns the account numbers a run should target, in order: --accounts,
// then --account, then the environment. It errors if nothing is configured or
// more than account_allowlist.MaxAccounts are requested.
func (a Accounts) Targets() ([]string, error) {
	raw := strings.TrimSpace(a.Accounts)
	if raw == "" {
		raw = strings.TrimSpace(a.Account)
	}
	if raw != "" {
		return account_allowlist.Parse(raw)
	}
	return account_allowlist.FromEnv()
}

// ResolveAccounts resolves targets through a live Schwab client. It never substitutes the
// Turso account_state mirror: cash, equity and positions that are hours old would size and
// check real orders wrongly. A job that is meant to run from the mirror says so by calling
// ResolveAccountsLocalOnly. Either the client cannot be built (token missing, expired or
// revoked) or Schwab refuses the account lookup, and the error says which and what to do.
func ResolveAccounts(ctx context.Context, cfg *appconfig.Config, db *sql.DB, targets []string) (*trader.TraderClient, []*trader.ResolvedAccount, error) {
	tc, err := trader.NewTraderClient(ctx, cfg.Schwab)
	if err != nil {
		return nil, nil, fmt.Errorf("no Schwab client, so accounts %s cannot be read live: %w (if the refresh token expired or was revoked, run `orchestrator auth`; check SCHWAB_APP_KEY and SCHWAB_APP_SECRET in the config object)", strings.Join(targets, ", "), err)
	}
	liveAccounts, err := tc.GetAccounts(ctx, targets)
	if err != nil {
		return nil, nil, fmt.Errorf("Schwab account lookup failed for %s: %w (the Turso account_state mirror is not used in its place; run `orchestrator account_state` once Schwab answers)", strings.Join(targets, ", "), err)
	}
	return tc, liveAccounts, nil
}

// ResolveAccountsLocalOnly resolves targets from the Turso account_state mirror only.
// Missing rows fail: never invent equity or cash values.
func ResolveAccountsLocalOnly(db *sql.DB, targets []string) ([]*trader.ResolvedAccount, error) {
	return account_allowlist.ResolveAllFromDB(db, targets)
}

// WriteMode prints the standard "Mode:" line for a job.
func WriteMode(out io.Writer, dryRun bool) {
	fmt.Fprintf(out, "Mode: %s\n", ModeLabel(dryRun))
}

// ModeLabel is the colored LIVE / DRY-RUN label.
func ModeLabel(dryRun bool) string {
	if dryRun {
		return fmt.Sprintf("%sDRY-RUN (Simulated)%s", ColGreen, ColReset)
	}
	return fmt.Sprintf("%sLIVE EXECUTION%s", ColRed, ColReset)
}
