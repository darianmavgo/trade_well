// Package account_allowlist validates which broker account numbers are
// permitted to trade and resolves one account's positions from the local
// account_state mirror. Renamed from the old internal/accounts package to
// avoid confusion with pkg/account_state, which owns the raw broker mirror
// this package reads from.
package account_allowlist

import (
	"fmt"
	"os"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/orchestrator/csvlist"
)

// EnvAccountNumbers is the comma-separated list of account numbers every
// job (including account_state.Sync) operates on.
const EnvAccountNumbers = "SCHWAB_ACCOUNT_NUMBERS"

// MaxAccounts is the largest number of accounts a single invocation
// may target. Raise it deliberately, not by working around it.
const MaxAccounts = 5

// Parse splits a comma-separated list of account numbers, trims whitespace,
// drops empty entries, and de-duplicates while preserving order. It fails
// explicitly rather than silently defaulting when the result is empty or
// exceeds MaxAccounts.
func Parse(raw string) ([]string, error) {
	out := csvlist.Unique(raw)

	if len(out) == 0 {
		return nil, fmt.Errorf("no Schwab account number(s) configured: pass --account/--accounts or set %s in the config object (deploy_gae/config.sh set %s=<number>[,<number>...])", EnvAccountNumbers, EnvAccountNumbers)
	}
	if len(out) > MaxAccounts {
		return nil, fmt.Errorf("%d account numbers configured (%s); maximum supported is %d", len(out), strings.Join(out, ", "), MaxAccounts)
	}
	return out, nil
}

// FromEnv resolves the allowlist from SCHWAB_ACCOUNT_NUMBERS, without logging.
func FromEnv() ([]string, error) {
	return Parse(strings.TrimSpace(os.Getenv(EnvAccountNumbers)))
}
