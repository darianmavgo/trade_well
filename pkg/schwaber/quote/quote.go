// Package quote is the `schwaber quote` command: a real-time market quote.
package quote

import (
	"context"
	"fmt"
	"io"

	"github.com/darianmavgo/trade_well/pkg/schwaber/clicolor"
	"github.com/darianmavgo/trade_well/pkg/schwaber/config"
	"github.com/darianmavgo/trade_well/pkg/schwaber/trader"
)

// Run prints the quote for ticker (already upper-cased) to out. Failures are
// printed to out and returned as clicolor.ErrReported.
func Run(ctx context.Context, out io.Writer, cfg *config.Config, ticker string) error {
	tc, err := trader.NewTraderClient(ctx, cfg)
	if err != nil {
		fmt.Fprintf(out, "%sError:%s %v\n", clicolor.Red, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	quote, err := tc.Client.GetQuote(ctx, ticker)
	if err != nil {
		fmt.Fprintf(out, "%sFailed to get quote for %s:%s %v\n", clicolor.Red, ticker, clicolor.Reset, err)
		return clicolor.ErrReported
	}

	lastPrice := trader.ExtractQuoteLastPrice(quote)
	fmt.Fprintf(out, "\n%s=== Market Quote - %s ===%s\n", clicolor.Bold, ticker, clicolor.Reset)
	fmt.Fprintf(out, "  Symbol:     %s\n", ticker)
	fmt.Fprintf(out, "  Price:      %s$%.2f%s\n", clicolor.Green, lastPrice, clicolor.Reset)
	fmt.Fprintf(out, "  Asset Type: %s\n", quote.AssetMainType)
	if quote.Realtime != nil {
		fmt.Fprintf(out, "  Realtime:   %v\n", *quote.Realtime)
	}
	return nil
}
