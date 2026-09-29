// Package accounts is the IBKR twin of schwaber/pkg/accounts: print the linked accounts.
package accounts

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/darianmavgo/ibkr_api/pkg/trader"
)

// Run prints each configured (or, with none configured, every linked) account.
func Run(ctx context.Context, out io.Writer, tc *trader.TraderClient) error {
	ids := tc.Config.AccountIDs
	if len(ids) == 0 {
		linked, err := tc.Client.ListLinkedAccounts(ctx)
		if err != nil {
			return fmt.Errorf("failed to list accounts: %w", err)
		}
		for _, a := range linked {
			ids = append(ids, a.AccountNumber)
		}
	}
	accs, err := tc.GetAccounts(ctx, ids)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "%-12s | %-14s | %18s | %18s | %10s\n", "Account #", "Type", "Cash Avail to Trade", "Total Account Value", "Positions")
	fmt.Fprintln(out, strings.Repeat("-", 86))
	for _, a := range accs {
		fmt.Fprintf(out, "%-12s | %-14s | %18.2f | %18.2f | %10d\n", a.AccountNumber, a.Details.Type,
			trader.GetBuyingPower(&a.Details), a.Details.CurrentBalances.LiquidationValue, len(a.Details.Positions))
	}
	return nil
}
