// Package accounts is the `schwaber accounts` command: list linked Schwab
// accounts with balances and, optionally, positions.
package accounts

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/darianmavgo/trade_well/pkg/schwaber/clicolor"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// Options are the inputs to Run.
type Options struct {
	Positions bool // also show each account's positions
}

// Run prints the linked accounts to out. Failures are printed to out and
// returned as clicolor.ErrReported; the caller decides the exit code.
func Run(ctx context.Context, out io.Writer, cfg *config.Config, opts Options) error {
	tc, err := trader.NewTraderClient(ctx, cfg)
	if err != nil {
		fmt.Fprintf(out, "%sError:%s %v\n", clicolor.Red, clicolor.Reset, err)
		fmt.Fprintf(out, "Run %sschwaber auth%s to connect your account.\n", clicolor.Cyan, clicolor.Reset)
		return clicolor.ErrReported
	}

	linked, err := tc.Client.ListLinkedAccounts(ctx)
	if err != nil {
		fmt.Fprintf(out, "%sFailed to list accounts:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	rothAcc, _ := tc.GetRothAccount(ctx)
	rothNum := ""
	if rothAcc != nil {
		rothNum = rothAcc.AccountNumber
	}

	fmt.Fprintf(out, "\n%s%s=== Linked Schwab Accounts ===%s\n", clicolor.Bold, clicolor.Cyan, clicolor.Reset)
	fmt.Fprintf(out, "%-12s | %-22s | %-8s | %18s | %18s\n", "Account #", "Designation", "Type", "Cash Avail to Trade", "Total Account Value")
	fmt.Fprintln(out, strings.Repeat("-", 90))

	for _, la := range linked {
		acc, err := tc.Client.GetAccount(ctx, la.HashValue, "positions")
		if err != nil {
			continue
		}
		sec, err := trader.ParseSecuritiesAccount(acc.SecuritiesAccount)
		if err != nil {
			continue
		}

		designation := "Brokerage"
		if sec.AccountNumber == rothNum {
			designation = fmt.Sprintf("%s★ ROTH IRA (Default)%s", clicolor.Yellow, clicolor.Reset)
		}

		cash := trader.GetBuyingPower(sec)
		totalVal := sec.CurrentBalances.LiquidationValue

		fmt.Fprintf(out, "%-12s | %-32s | %-8s | %18s | %18s\n",
			sec.AccountNumber,
			designation,
			sec.Type,
			fmt.Sprintf("$%.2f", cash),
			fmt.Sprintf("$%.2f", totalVal),
		)

		if opts.Positions {
			if len(sec.Positions) > 0 {
				fmt.Fprintf(out, "\n  %sPositions for Account %s:%s\n", clicolor.Blue, sec.AccountNumber, clicolor.Reset)
				fmt.Fprintf(out, "  %-8s | %10s | %12s | %14s | %12s\n", "Symbol", "Quantity", "Avg Price", "Market Value", "Day P/L ($)")
				fmt.Fprintf(out, "  %s\n", strings.Repeat("-", 66))
				for _, pos := range sec.Positions {
					plSign := ""
					plCol := clicolor.Green
					if pos.CurrentDayProfitLoss < 0 {
						plCol = clicolor.Red
					} else if pos.CurrentDayProfitLoss > 0 {
						plSign = "+"
					}
					fmt.Fprintf(out, "  %-8s | %10.2f | $%11.2f | $%13.2f | %s%s$%.2f%s\n",
						pos.Instrument.Symbol,
						pos.LongQuantity,
						pos.AveragePrice,
						pos.MarketValue,
						plCol, plSign, pos.CurrentDayProfitLoss, clicolor.Reset,
					)
				}
				fmt.Fprintln(out)
			}
		}
	}
	return nil
}
